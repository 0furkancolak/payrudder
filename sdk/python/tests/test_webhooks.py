from __future__ import annotations

import hashlib
import hmac
import json
import re
from pathlib import Path

import pytest

from payrudder import EVENT_TYPES, EventType, WebhookSignatureError, construct_event, verify_webhook

SECRET = "whsec_test_secret"
NOW = 1_700_000_000
BODY = json.dumps(
    {
        "id": "0192",
        "type": "invoice.paid",
        "created": "2026-10-20T10:00:00Z",
        "tenant_id": "t1",
        "data": {"invoice_id": "i1", "total_minor": "9007199254740993", "tax_minor": "186", "environment": "live"},
    }
).encode()


def sign(body: bytes, secret: str = SECRET, t: int = NOW) -> str:
    return hmac.new(secret.encode(), f"{t}.".encode() + body, hashlib.sha256).hexdigest()


def header(*sigs: str, t: int = NOW) -> str:
    return f"t={t}," + ",".join(f"v1={s}" for s in sigs)


def test_valid_signature() -> None:
    verify_webhook(BODY, header(sign(BODY)), SECRET, now=NOW)
    verify_webhook(BODY.decode(), header(sign(BODY)), SECRET, now=NOW + 299)


def test_tampered_payload_is_rejected() -> None:
    with pytest.raises(WebhookSignatureError, match="no signature matches"):
        verify_webhook(BODY + b" ", header(sign(BODY)), SECRET, now=NOW)


def test_wrong_secret_is_rejected() -> None:
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, header(sign(BODY, "whsec_other")), SECRET, now=NOW)


def test_tampered_timestamp_is_rejected() -> None:
    forged = f"t={NOW + 1},v1={sign(BODY)}"
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, forged, SECRET, now=NOW)


@pytest.mark.parametrize("skew", [301, -301, 3600])
def test_stale_or_future_timestamp_is_rejected(skew: int) -> None:
    with pytest.raises(WebhookSignatureError, match="tolerance"):
        verify_webhook(BODY, header(sign(BODY)), SECRET, now=NOW + skew)


def test_tolerance_is_configurable() -> None:
    verify_webhook(BODY, header(sign(BODY)), SECRET, tolerance=3600, now=NOW + 3000)
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, header(sign(BODY)), SECRET, tolerance=10, now=NOW + 11)


def test_rotated_secret_either_signature_verifies() -> None:
    both = header(sign(BODY, "whsec_old"), sign(BODY, SECRET))
    verify_webhook(BODY, both, SECRET, now=NOW)
    verify_webhook(BODY, both, "whsec_old", now=NOW)
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, both, "whsec_neither", now=NOW)


@pytest.mark.parametrize("bad", [None, "", "garbage", "t=abc,v1=00", f"t={NOW}", f"v1={'0' * 64}", f"t={NOW},v2=00"])
def test_malformed_headers(bad: str | None) -> None:
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, bad, SECRET, now=NOW)


def test_signature_with_odd_hex_does_not_crash() -> None:
    with pytest.raises(WebhookSignatureError):
        verify_webhook(BODY, f"t={NOW},v1=zz", SECRET, now=NOW)


def test_construct_event_parses_and_decodes_money() -> None:
    event = construct_event(BODY, header(sign(BODY)), SECRET, now=NOW)
    assert event["type"] == EventType.INVOICE_PAID
    assert event["data"]["total_minor"] == 9007199254740993
    assert event["data"]["tax_minor"] == 186
    assert event["data"]["invoice_id"] == "i1"


def test_construct_event_refuses_unverified_and_non_events() -> None:
    with pytest.raises(WebhookSignatureError):
        construct_event(BODY, header(sign(BODY, "x")), SECRET, now=NOW)
    junk = b'["not", "an", "event"]'
    with pytest.raises(WebhookSignatureError, match="envelope"):
        construct_event(junk, header(sign(junk)), SECRET, now=NOW)


def test_default_clock_is_used() -> None:
    import time

    t = int(time.time())
    construct_event(BODY, header(sign(BODY, t=t), t=t), SECRET)


def test_event_type_constants_cover_the_documented_table() -> None:
    doc = Path(__file__).resolve().parents[3] / "docs" / "16-webhooks.md"
    if not doc.exists():
        pytest.skip("docs/16-webhooks.md is not next to the SDK")
    text = doc.read_text()
    table = text.split("## Event types", 1)[1].split("A delivery is a JSON", 1)[0]
    documented = set()
    for row in table.splitlines():
        if row.startswith("|") and not row.startswith("|---"):
            documented |= set(re.findall(r"`([a-z_]+\.[a-z_]+)`", row.split("|")[1]))
    assert documented, "no event types parsed from the docs"
    assert documented <= set(EVENT_TYPES), sorted(documented - set(EVENT_TYPES))
    assert "ping" in EVENT_TYPES


def test_event_data_types_cover_each_event_family() -> None:
    from typing import cast, get_type_hints

    from payrudder import (
        CreditNoteEventData,
        CreditsLowEventData,
        CreditsPurchasedEventData,
        InvoiceEventData,
        PaymentLinkEventData,
        SubscriptionEventData,
    )

    event = construct_event(BODY, header(sign(BODY)), SECRET, now=NOW)
    data = cast(InvoiceEventData, event["data"])
    assert data["invoice_id"] == "i1"
    assert data["total_minor"] == 9007199254740993
    assert data["tax_minor"] == 186
    assert InvoiceEventData.__required_keys__ == {"invoice_id", "total_minor"}
    assert CreditNoteEventData.__required_keys__ == {"credit_note_id", "total_minor"}
    assert SubscriptionEventData.__required_keys__ == {"subscription"}
    assert PaymentLinkEventData.__required_keys__ == {"link_id"}
    assert CreditsPurchasedEventData.__required_keys__ == {"customer_id", "credits", "balance"}
    assert CreditsLowEventData.__required_keys__ == {"customer_id", "balance", "threshold"}
    assert "net_minor" in get_type_hints(InvoiceEventData)
