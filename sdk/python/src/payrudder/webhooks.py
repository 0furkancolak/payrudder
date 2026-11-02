"""Verify and parse webhook deliveries (docs/16-webhooks.md).

Every delivery carries ``Payrudder-Signature: t=<unix>,v1=<hex>[,v1=<hex>]`` where each ``v1`` is
``HMAC-SHA256(secret, "<t>.<raw body>")``. Verify against the **raw** request bytes, before parsing the JSON.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import time
from typing import Any, Literal, TypedDict

from . import _wire
from .errors import WebhookSignatureError
from .models import ChangeQuote, Subscription

SIGNATURE_HEADER = "Payrudder-Signature"
EVENT_ID_HEADER = "Payrudder-Event-Id"
EVENT_TYPE_HEADER = "Payrudder-Event-Type"
DELIVERY_ATTEMPT_HEADER = "Payrudder-Delivery-Attempt"
DEFAULT_TOLERANCE = 300


class EventType:
    """The event type names a delivery's ``type`` can have."""

    INVOICE_FINALIZED = "invoice.finalized"
    INVOICE_PAID = "invoice.paid"
    INVOICE_VOIDED = "invoice.voided"
    INVOICE_UNCOLLECTIBLE = "invoice.uncollectible"
    CREDIT_NOTE_CREATED = "credit_note.created"
    CREDIT_NOTE_REFUNDED = "credit_note.refunded"
    CREDIT_NOTE_REFUND_FAILED = "credit_note.refund_failed"
    SUBSCRIPTION_CREATED = "subscription.created"
    SUBSCRIPTION_CHANGED = "subscription.changed"
    SUBSCRIPTION_CHANGE_SCHEDULED = "subscription.change_scheduled"
    SUBSCRIPTION_CANCELLED = "subscription.cancelled"
    SUBSCRIPTION_CANCELLATION_SCHEDULED = "subscription.cancellation_scheduled"
    SUBSCRIPTION_CANCELLATION_WITHDRAWN = "subscription.cancellation_withdrawn"
    SUBSCRIPTION_PAUSED = "subscription.paused"
    SUBSCRIPTION_RESUMED = "subscription.resumed"
    SUBSCRIPTION_MOVED_TO_PROVIDER = "subscription.moved_to_provider"
    SUBSCRIPTION_TRIAL_WILL_END = "subscription.trial_will_end"
    PAYMENT_LINK_CREATED = "payment_link.created"
    PAYMENT_LINK_PAID = "payment_link.paid"
    PAYMENT_LINK_EXPIRED = "payment_link.expired"
    PAYMENT_DISPUTED = "payment.disputed"
    CREDITS_PURCHASED = "credits.purchased"
    CREDITS_LOW = "credits.low"
    CUSTOMER_BLOCKED = "customer.blocked"
    CUSTOMER_UNBLOCKED = "customer.unblocked"
    PRICE_CHANGE_SCHEDULED = "price_change.scheduled"
    CONTRACT_ACCEPTED = "contract.accepted"
    CONTRACT_VERSION_PUBLISHED = "contract.version_published"
    CONTRACT_WITHDRAWAL_REQUESTED = "contract.withdrawal_requested"
    CONTRACT_WITHDRAWAL_DECIDED = "contract.withdrawal_decided"
    PING = "ping"  # sent by POST /v1/webhook-endpoints/{id}/test


EVENT_TYPES: tuple[str, ...] = tuple(v for k, v in vars(EventType).items() if k.isupper())


class _InvoiceEventDataRequired(TypedDict):
    invoice_id: str
    total_minor: int


class InvoiceEventData(_InvoiceEventDataRequired, total=False):
    """``data`` of ``invoice.*`` events."""

    number: str
    customer_id: str
    customer_external_id: str
    period_id: str
    currency: str
    status: str
    environment: str
    tax_minor: int  # invoice.paid only
    net_minor: int  # invoice.paid only: the total less its TAX lines


class _CreditNoteEventDataRequired(TypedDict):
    credit_note_id: str
    total_minor: int


class CreditNoteEventData(_CreditNoteEventDataRequired, total=False):
    """``data`` of ``credit_note.*`` events."""

    number: str
    invoice_id: str
    invoice_number: str
    customer_id: str
    customer_external_id: str
    currency: str
    reason: str
    disposition: Literal["credit", "refund"]
    refund_state: str
    refund_error: str


class _SubscriptionEventDataRequired(TypedDict):
    subscription: Subscription


class SubscriptionEventData(_SubscriptionEventDataRequired, total=False):
    """``data`` of ``subscription.*`` events."""

    previous_subscription_id: str
    quote: ChangeQuote
    to_provider: str
    credited_minor: int


class _PaymentLinkEventDataRequired(TypedDict):
    link_id: str


class PaymentLinkEventData(_PaymentLinkEventDataRequired, total=False):
    """``data`` of ``payment_link.*`` events."""

    kind: str
    invoice_id: str
    invoice_number: str
    amount_minor: int
    total_minor: int
    currency: str
    description: str
    customer_external_id: str
    single_use: bool
    expires_at: str
    paid_count: int


class _CreditsPurchasedEventDataRequired(TypedDict):
    customer_id: str
    credits: str
    balance: str


class CreditsPurchasedEventData(_CreditsPurchasedEventDataRequired, total=False):
    """``data`` of ``credits.purchased``."""

    customer_external_id: str
    bonus_credits: str
    quantity: int
    pack_id: str
    pack_name: str
    link_id: str
    invoice_id: str
    invoice_number: str
    currency: str
    amount_minor: int
    expires_at: str


class _CreditsLowEventDataRequired(TypedDict):
    customer_id: str
    balance: str
    threshold: str


class CreditsLowEventData(_CreditsLowEventDataRequired, total=False):
    """``data`` of ``credits.low``."""

    customer_external_id: str


class _PaymentDisputedEventDataRequired(TypedDict):
    invoice_id: str
    invoice_number: str
    customer_external_id: str
    provider: str
    dispute_id: str
    payment_ref: str
    amount_minor: int
    currency: str
    outcome: str
    formal: bool
    reason: str
    provider_status: str


class PaymentDisputedEventData(_PaymentDisputedEventDataRequired, total=False):
    """``data`` of ``payment.disputed``: one step of a case a customer opened with their bank about a payment.

    ``dispute_id`` is the provider's reference of the case, the same in every event about it; ``outcome`` is where it
    stands (``open``, ``won`` or ``lost``); ``formal`` is true for a chargeback, which takes the money back, and false
    for an inquiry or a warning ahead of one; ``reason`` is the cardholder's, in the provider's words, and may be empty.
    ``evidence_due_time`` (RFC 3339) is set when the provider gives a date.
    """

    evidence_due_time: str


class _CustomerBlockEventDataRequired(TypedDict):
    customer_id: str


class CustomerBlockEventData(_CustomerBlockEventDataRequired, total=False):
    """``data`` of ``customer.blocked`` and ``customer.unblocked``.

    ``category`` (fraud, chargeback, abuse, request, other, or empty), ``reason`` (the seller's own note) and
    ``blocked_until`` (an RFC 3339 time, absent when the
    block lasts until it is lifted) are set on ``customer.blocked`` only, and so is ``suspend_service`` (the block also
    suspended the customer's service; absent means false).
    """

    customer_external_id: str
    category: str
    reason: str
    blocked_until: str
    suspend_service: bool


class _WebhookPriceChangeRequired(TypedDict):
    id: str
    plan_code: str
    from_version: int
    to_version: int
    notice_days: int
    effective_time: str
    state: str
    create_time: str


class WebhookPriceChange(_WebhookPriceChangeRequired, total=False):
    """A price change in an event: as the API returns it, without the fields at their zero value. Times are RFC 3339.

    ``effective_time`` is the first date a customer's price changes and ``effective_from`` (when present) the date the
    seller chose it to start from; ``state`` is ``open`` or ``withdrawn``;
    ``scheduled``, ``applied``, ``withdrawn`` and ``skipped`` count the subscriptions waiting, moved, withdrawn and left
    out.
    """

    effective_from: str
    created_by: str
    withdraw_time: str
    withdrawn_by: str
    scheduled: int
    applied: int
    withdrawn: int
    skipped: int


class PriceChangeScheduledEventData(TypedDict):
    """``data`` of ``price_change.scheduled``: a run of a price change scheduled at least one subscription.

    ``scheduled`` and ``skipped`` are what that run did.
    """

    price_change: WebhookPriceChange
    scheduled: int
    skipped: int


class _ContractAcceptedItem(TypedDict):
    acceptance_id: str
    kind: str
    audience: str
    version_id: str
    version: int
    sha256: str
    locale: str


class _ContractContext(TypedDict):
    kind: str
    ref: str


class _ContractAcceptedEventDataRequired(TypedDict):
    group_id: str
    source: str
    accepted_at: str
    snapshot_sha256: str
    immediate_start_consent: bool
    withdrawal_waiver_acknowledged: bool
    context: _ContractContext
    items: list[_ContractAcceptedItem]


class ContractAcceptedEventData(_ContractAcceptedEventDataRequired, total=False):
    """``data`` of ``contract.accepted``: one event for the versions a customer accepted together."""

    customer_id: str  # absent for a guest payment
    customer_external_id: str


class ContractVersionPublishedEventData(TypedDict):
    """``data`` of ``contract.version_published``."""

    version_id: str
    kind: str
    audience: str
    number: int
    material: bool
    sha256: str
    locales: list[str]
    published_at: str


class ContractWithdrawalRequestedEventData(TypedDict):
    """``data`` of ``contract.withdrawal_requested``."""

    withdrawal_id: str
    customer_id: str
    customer_external_id: str
    subscription_id: str
    invoice_id: str
    source: str
    received_at: str


class ContractWithdrawalDecidedEventData(TypedDict):
    """``data`` of ``contract.withdrawal_decided``."""

    withdrawal_id: str
    customer_id: str
    customer_external_id: str
    subscription_id: str
    invoice_id: str
    status: str
    decided_by: str
    decided_at: str
    note: str


class WebhookEvent(TypedDict):
    """A delivery's JSON body. ``data`` depends on ``type``; its ``*_minor`` values are ints.

    ``data`` stays a plain dict so one handler can take every type; narrow it with ``typing.cast`` to the
    ``*EventData`` class of the type (``invoice.*`` is ``InvoiceEventData``, ``credit_note.*`` is
    ``CreditNoteEventData``, ``subscription.*`` is ``SubscriptionEventData``, ``payment_link.*`` is
    ``PaymentLinkEventData``, ``payment.disputed`` is ``PaymentDisputedEventData``, ``credits.purchased`` /
    ``credits.low`` are ``CreditsPurchasedEventData`` /
    ``CreditsLowEventData``, ``customer.blocked`` / ``customer.unblocked`` are ``CustomerBlockEventData``,
    ``price_change.scheduled`` is
    ``PriceChangeScheduledEventData``).
    ``CreditsLowEventData``, ``customer.blocked`` / ``customer.unblocked`` are ``CustomerBlockEventData``;
    ``contract.accepted`` / ``contract.version_published`` /
    ``contract.withdrawal_requested`` / ``contract.withdrawal_decided`` are ``ContractAcceptedEventData`` /
    ``ContractVersionPublishedEventData`` / ``ContractWithdrawalRequestedEventData`` /
    ``ContractWithdrawalDecidedEventData``).
    """

    id: str
    type: str
    created: str
    tenant_id: str
    data: dict[str, Any]


def _parse_header(header: str) -> tuple[int, list[str]]:
    timestamp: int | None = None
    signatures: list[str] = []
    for part in header.split(","):
        name, sep, value = part.strip().partition("=")
        if not sep:
            continue
        if name == "t":
            try:
                timestamp = int(value)
            except ValueError:
                raise WebhookSignatureError("the signature header has a non-numeric timestamp") from None
        elif name == "v1":
            signatures.append(value.strip())
    if timestamp is None:
        raise WebhookSignatureError("the signature header has no timestamp")
    if not signatures:
        raise WebhookSignatureError("the signature header has no v1 signature")
    return timestamp, signatures


def verify_webhook(
    payload: bytes | str,
    header: str | None,
    secret: str | bytes,
    tolerance: float = DEFAULT_TOLERANCE,
    *,
    now: float | None = None,
) -> None:
    """Raise :class:`WebhookSignatureError` unless ``header`` signs ``payload`` with ``secret``.

    Args:
        payload: the raw request body. Pass the bytes exactly as received; a re-serialised JSON will not match.
        header: the value of the ``Payrudder-Signature`` header.
        secret: the endpoint's ``whsec_...`` secret. During a rotation either the new or the old one verifies,
            because the delivery then carries two ``v1`` signatures.
        tolerance: seconds the timestamp may differ from the clock, in either direction (default 300).
        now: the clock, for tests.
    """
    if not header:
        raise WebhookSignatureError("missing Payrudder-Signature header")
    timestamp, signatures = _parse_header(header)
    clock = time.time() if now is None else now
    if abs(clock - timestamp) > tolerance:
        raise WebhookSignatureError("the signature timestamp is outside the tolerance")
    raw = payload.encode() if isinstance(payload, str) else payload
    key = secret.encode() if isinstance(secret, str) else secret
    expected = hmac.new(key, f"{timestamp}.".encode() + raw, hashlib.sha256).hexdigest()
    if not any(hmac.compare_digest(sig.lower(), expected) for sig in signatures):
        raise WebhookSignatureError("no signature matches the payload")


def construct_event(
    payload: bytes | str,
    header: str | None,
    secret: str | bytes,
    tolerance: float = DEFAULT_TOLERANCE,
    *,
    now: float | None = None,
) -> WebhookEvent:
    """Verify the delivery (see :func:`verify_webhook`) and parse it into a :class:`WebhookEvent`."""
    verify_webhook(payload, header, secret, tolerance, now=now)
    try:
        parsed = json.loads(payload)
    except ValueError as exc:
        raise WebhookSignatureError(f"the verified payload is not JSON: {exc}") from exc
    if not isinstance(parsed, dict) or not isinstance(parsed.get("id"), str) or not isinstance(parsed.get("type"), str):
        raise WebhookSignatureError("the verified payload is not an event envelope")
    event: WebhookEvent = _wire.decode(parsed)
    event.setdefault("data", {})
    return event
