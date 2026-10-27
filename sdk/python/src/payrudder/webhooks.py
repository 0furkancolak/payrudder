"""Verify and parse webhook deliveries (docs/16-webhooks.md).

Every delivery carries ``Payrudder-Signature: t=<unix>,v1=<hex>[,v1=<hex>]`` where each ``v1`` is
``HMAC-SHA256(secret, "<t>.<raw body>")``. Verify against the **raw** request bytes, before parsing the JSON.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import time
from typing import Any, TypedDict

from . import _wire
from .errors import WebhookSignatureError

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
    CREDITS_PURCHASED = "credits.purchased"
    CREDITS_LOW = "credits.low"
    PING = "ping"  # sent by POST /v1/webhook-endpoints/{id}/test


EVENT_TYPES: tuple[str, ...] = tuple(v for k, v in vars(EventType).items() if k.isupper())


class WebhookEvent(TypedDict):
    """A delivery's JSON body. ``data`` depends on ``type``; its ``*_minor`` values are ints."""

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
