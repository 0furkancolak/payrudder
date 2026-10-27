from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal

import httpx
import pytest

from payrudder import (
    APIConnectionError,
    APIError,
    AsyncPayrudder,
    AuthenticationError,
    ConfigurationError,
    ConflictError,
    NotFoundError,
    Payrudder,
    PermissionDeniedError,
    RateLimitError,
    ServerError,
    ValidationError,
    __version__,
)

from .conftest import FakeGateway, err, new_customer, ok


def test_version_is_prerelease() -> None:
    assert __version__ == "0.0.0"


def test_api_key_and_base_url_come_from_the_environment(monkeypatch: pytest.MonkeyPatch, gateway: FakeGateway) -> None:
    monkeypatch.setenv("PAYRUDDER_API_KEY", "qk_test_env_key")
    monkeypatch.setenv("PAYRUDDER_BASE_URL", gateway.url + "/")
    gateway.script = [ok({"id": "1", "external_id": "c1"})]
    with Payrudder() as c:
        assert c.base_url == gateway.url
        c.customers.get_customer("c1")
    assert gateway.requests[0].headers["authorization"] == "Bearer qk_test_env_key"


def test_missing_api_key_is_a_configuration_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("PAYRUDDER_API_KEY", raising=False)
    with pytest.raises(ConfigurationError):
        Payrudder()
    with pytest.raises(ConfigurationError):
        AsyncPayrudder()


def test_default_base_url(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("PAYRUDDER_BASE_URL", raising=False)
    assert Payrudder("qk_test_x_y").base_url == "http://localhost:42800"


def test_headers_path_and_query(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok({"customers": []})]
    client.customers.list_customers(page_size=50, page_token="tok/1")
    req = gateway.requests[0]
    assert req.method == "GET"
    assert req.path == "/v1/customers"
    assert req.query == {"page_size": "50", "page_token": "tok/1"}
    assert req.headers["authorization"] == "Bearer qk_test_abcd1234_secret"
    assert req.headers["user-agent"] == "payrudder-python/0.0.0"
    assert req.headers["accept"] == "application/json"


def test_path_parameters_are_escaped_and_must_not_be_empty(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok({})]
    client.customers.get_customer("a/b c")
    assert gateway.requests[0].path == "/v1/customers/a%2Fb%20c"
    with pytest.raises(ValueError, match="empty"):
        client.customers.get_customer("")


def test_python_keyword_query_parameter_is_sent_under_its_wire_name(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok({"currencies": []})]
    client.accounting.get_accounting_summary(from_=date(2026, 1, 1), to="2026-01-31")
    assert gateway.requests[0].query["from"] == "2026-01-01"
    assert gateway.requests[0].query["to"] == "2026-01-31"


def test_int64_strings_become_ints_and_back(client: Payrudder, gateway: FakeGateway) -> None:
    big = 9007199254740993  # 2**53 + 1: a float would round it
    gateway.script = [ok({"invoices": [{"id": "i1", "total_minor": str(big), "subtotal_minor": "-5"}]})]
    page = client.invoices.list_invoices()
    assert page["invoices"][0]["total_minor"] == big
    assert page["invoices"][0]["subtotal_minor"] == -5

    gateway.script = [ok({}, status=201)]
    client.payment_links.create_payment_link({"kind": "adhoc", "amount_minor": big})
    assert gateway.requests[-1].json()["amount_minor"] == str(big)


def test_decimal_and_datetime_are_sent_as_text(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok({"results": [{"status": "accepted"}]}, status=202)]
    client.events.ingest_events(
        {
            "events": [
                {
                    "customer_external_id": "c",
                    "meter_code": "m",
                    "quantity": Decimal("1.250"),
                    "event_time": datetime(2026, 11, 3, 10, 15, tzinfo=timezone.utc),
                    "idempotency_key": "k1",
                }
            ]
        }
    )
    sent = gateway.requests[0].json()["events"][0]
    assert sent["quantity"] == "1.250"
    assert sent["event_time"] == "2026-11-03T10:15:00Z"


def test_float_quantities_are_refused(client: Payrudder) -> None:
    with pytest.raises(TypeError, match="float"):
        client.events.ingest_events(
            {
                "events": [
                    {
                        "customer_external_id": "c",
                        "meter_code": "m",
                        "quantity": 1.5,  # type: ignore[typeddict-item]
                        "event_time": "2026-01-01T00:00:00Z",
                    }
                ]
            }
        )


def test_no_content_text_and_bytes_responses(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok(status=204)]
    assert client.coupons.deactivate_coupon("X") is None
    gateway.script = [ok("a,b\n1,2\n")]
    assert client.accounting.export_accounting_csv(from_="2026-01-01", to="2026-01-31") == "a,b\n1,2\n"
    assert gateway.requests[-1].headers["accept"] == "*/*"
    gateway.script = [ok(b"%PDF-1.7")]
    assert client.expense_inbox.get_inbox_item_file("0192") == b"%PDF-1.7"


def test_multipart_upload(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok({"results": []})]
    client.assets.upload_assets(
        [("logo.png", b"\x89PNG", "image/png"), ("a.txt", b"hi")],
        metadata={"files": {"logo.png": {"purpose": "invoice_logo"}}},
    )
    req = gateway.requests[0]
    assert req.headers["content-type"].startswith("multipart/form-data; boundary=")
    assert b'name="metadata"' in req.body
    assert b'filename="logo.png"' in req.body
    assert b"image/png" in req.body
    assert b'filename="a.txt"' in req.body


@pytest.mark.parametrize(
    ("status", "cls"),
    [
        (400, ValidationError),
        (401, AuthenticationError),
        (403, PermissionDeniedError),
        (404, NotFoundError),
        (409, ConflictError),
        (422, ValidationError),
        (429, RateLimitError),
        (500, ServerError),
        (503, ServerError),
        (418, APIError),
    ],
)
def test_error_mapping(client: Payrudder, gateway: FakeGateway, status: int, cls: type[APIError]) -> None:
    gateway.script = [err(status, "INVALID_ARGUMENT", "billing.credit_note.nothing_to_refund", **{"Retry-After": "7"})]
    with pytest.raises(cls) as info:
        client.customers.get_customer("c", options={"max_retries": 0})
    e = info.value
    assert type(e) is cls
    assert e.status_code == status
    assert e.code == "INVALID_ARGUMENT"
    assert e.key == "billing.credit_note.nothing_to_refund"
    assert e.message == f"boom {status}"
    assert e.request_id == "req_1"
    assert e.retry_after == 7.0
    assert f"{status}" in str(e)


def test_validation_details_are_kept(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [
        ok(
            {
                "error": {
                    "code": "INVALID_ARGUMENT",
                    "key": "common.invalid_argument",
                    "message": "bad",
                    "details": [{"field": "name", "description": "required"}],
                }
            },
            status=400,
            **{"X-Request-Id": "hdr_9"},
        )
    ]
    with pytest.raises(ValidationError) as info:
        client.customers.create_customer(new_customer("x"))
    assert info.value.details == [{"field": "name", "description": "required"}]
    assert info.value.request_id == "hdr_9"


def test_non_json_error_body_still_maps(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [ok("<html>bad gateway</html>", status=502)] * 3
    with pytest.raises(ServerError) as info:
        client.customers.get_customer("c")
    assert info.value.status_code == 502
    assert "bad gateway" in info.value.message


def test_connection_error(sleeps: object) -> None:
    c = Payrudder("qk_test_x_y", base_url="http://127.0.0.1:1", max_retries=0)
    with pytest.raises(APIConnectionError):
        c.customers.get_customer("c")


def test_injected_transport(sleeps: object) -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/v1/customers/c"
        return httpx.Response(200, json={"id": "1", "external_id": "c"})

    c = Payrudder("qk_test_x_y", transport=httpx.MockTransport(handler))
    assert c.customers.get_customer("c")["external_id"] == "c"
    with pytest.raises(ValueError, match="not both"):
        Payrudder("k", transport=httpx.MockTransport(handler), http_client=httpx.Client())
