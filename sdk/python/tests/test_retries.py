from __future__ import annotations

import pytest

from payrudder import (
    APIConnectionError,
    AsyncPayrudder,
    NotFoundError,
    Payrudder,
    RateLimitError,
    ServerError,
    ValidationError,
)

from .conftest import FakeGateway, Reply, Sleeps, err, new_customer, ok

CUSTOMER = {"id": "1", "external_id": "c"}


def test_retry_after_is_honoured_on_429(client: Payrudder, gateway: FakeGateway, sleeps: Sleeps) -> None:
    gateway.script = [err(429, "RESOURCE_EXHAUSTED", **{"Retry-After": "3"}), ok(CUSTOMER)]
    assert client.customers.get_customer("c")["external_id"] == "c"
    assert len(gateway.requests) == 2
    assert sleeps.delays == [3.0]


@pytest.mark.parametrize("status", [502, 503, 504])
def test_gateway_errors_are_retried_even_for_post(
    client: Payrudder, gateway: FakeGateway, sleeps: Sleeps, status: int
) -> None:
    gateway.script = [err(status), ok(CUSTOMER, status=201)]
    client.customers.create_customer(new_customer("c"))
    assert len(gateway.requests) == 2
    assert len(sleeps.delays) == 1


def test_backoff_is_exponential_jittered_and_capped(client: Payrudder, gateway: FakeGateway, sleeps: Sleeps) -> None:
    gateway.script = [err(503)] * 5
    with pytest.raises(ServerError):
        client.customers.get_customer("c", options={"max_retries": 4})
    assert len(gateway.requests) == 5
    assert len(sleeps.delays) == 4
    for attempt, delay in enumerate(sleeps.delays):
        ceiling = min(8.0, 0.5 * 2**attempt)
        assert ceiling * 0.5 <= delay <= ceiling


def test_gives_up_after_max_retries(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(503)] * 10
    with pytest.raises(ServerError):
        client.customers.get_customer("c")
    assert len(gateway.requests) == 3  # 1 + the default 2 retries


def test_long_retry_after_is_not_waited_for(client: Payrudder, gateway: FakeGateway, sleeps: Sleeps) -> None:
    gateway.script = [err(429, "RESOURCE_EXHAUSTED", **{"Retry-After": "3600"})]
    with pytest.raises(RateLimitError) as info:
        client.customers.get_customer("c")
    assert info.value.retry_after == 3600
    assert sleeps.delays == []


def test_500_is_retried_for_get_but_not_for_a_bare_post(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(500, "INTERNAL"), ok(CUSTOMER)]
    client.customers.get_customer("c")
    assert len(gateway.requests) == 2

    gateway.requests.clear()
    gateway.script = [err(500, "INTERNAL"), ok(CUSTOMER, status=201)]
    with pytest.raises(ServerError):
        client.customers.create_customer(new_customer("c"))
    assert len(gateway.requests) == 1


def test_500_is_retried_for_a_post_that_carries_an_idempotency_key(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(500, "INTERNAL"), ok({"id": "l1", "token": "t"}, status=201)]
    client.payment_links.create_payment_link(
        {"kind": "adhoc", "amount_minor": 500}, options={"idempotency_key": "order-77"}
    )
    assert len(gateway.requests) == 2
    assert [r.headers["idempotency-key"] for r in gateway.requests] == ["order-77", "order-77"]
    assert gateway.requests[0].body == gateway.requests[1].body


def test_500_is_retried_for_put_and_delete(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(500, "INTERNAL"), ok(CUSTOMER)]
    client.customers.set_customer_tax("c", {"tax_id": "1"})
    gateway.script = [err(500, "INTERNAL"), ok(status=204)]
    client.coupons.deactivate_coupon("X")
    assert len(gateway.requests) == 4


@pytest.mark.parametrize("status", [400, 401, 403, 404, 409, 422])
def test_other_4xx_are_never_retried(client: Payrudder, gateway: FakeGateway, status: int) -> None:
    gateway.script = [err(status), ok(CUSTOMER)]
    with pytest.raises(Exception) as info:  # noqa: B017 - each status maps to its own class, asserted elsewhere
        client.customers.get_customer("c")
    assert getattr(info.value, "status_code", None) == status
    assert len(gateway.requests) == 1


def test_network_error_is_retried_for_get(client: Payrudder, gateway: FakeGateway, sleeps: Sleeps) -> None:
    gateway.script = [Reply(drop=True), ok(CUSTOMER)]
    assert client.customers.get_customer("c")["external_id"] == "c"
    assert len(gateway.requests) == 2
    assert len(sleeps.delays) == 1


def test_lost_connection_is_not_retried_for_a_bare_post(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [Reply(drop=True), ok(CUSTOMER, status=201)]
    with pytest.raises(APIConnectionError):
        client.customers.create_customer(new_customer("c"))
    assert len(gateway.requests) == 1


def test_refused_connection_is_retried_then_raised(sleeps: Sleeps) -> None:
    c = Payrudder("qk_test_x_y", base_url="http://127.0.0.1:1")
    c._sleep = sleeps.sync
    with pytest.raises(APIConnectionError):
        c.customers.create_customer(new_customer("c"))  # never reached the server, so a POST may be retried
    assert len(sleeps.delays) == 2


def test_validation_error_carries_through(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(400)]
    with pytest.raises(ValidationError):
        client.customers.create_customer(new_customer(""))
    gateway.script = [err(404, "NOT_FOUND", "common.not_found")]
    with pytest.raises(NotFoundError):
        client.customers.get_customer("nope")


def test_ingest_keys_are_stable_across_retries(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(500, "INTERNAL"), ok({"results": [{"status": "accepted", "event_id": "e1"}]}, status=202)]
    results = client.events.ingest(
        [{"customer_external_id": "c", "meter_code": "m", "quantity": "1", "event_time": "2026-01-01T00:00:00Z"}]
    )
    assert results == [{"status": "accepted", "event_id": "e1"}]
    assert len(gateway.requests) == 2  # a 500 is retried because every event carries a key
    first, second = (r.json()["events"][0]["idempotency_key"] for r in gateway.requests)
    assert first == second
    assert first


async def test_async_retry_after_and_errors(aclient: AsyncPayrudder, gateway: FakeGateway, sleeps: Sleeps) -> None:
    gateway.script = [err(429, "RESOURCE_EXHAUSTED", **{"Retry-After": "2"}), ok(CUSTOMER)]
    assert (await aclient.customers.get_customer("c"))["external_id"] == "c"
    assert sleeps.delays == [2.0]
    gateway.script = [err(404, "NOT_FOUND", "common.not_found")]
    with pytest.raises(NotFoundError):
        await aclient.customers.get_customer("nope")


async def test_async_500_rules_and_network_errors(aclient: AsyncPayrudder, gateway: FakeGateway) -> None:
    gateway.script = [err(500, "INTERNAL"), ok({"customers": []})]
    await aclient.customers.list_customers()
    assert len(gateway.requests) == 2
    gateway.requests.clear()
    gateway.script = [err(500, "INTERNAL"), ok(CUSTOMER, status=201)]
    with pytest.raises(ServerError):
        await aclient.customers.create_customer(new_customer("c"))
    assert len(gateway.requests) == 1
    gateway.requests.clear()
    gateway.script = [Reply(drop=True), ok(CUSTOMER)]
    await aclient.customers.get_customer("c")
    assert len(gateway.requests) == 2
