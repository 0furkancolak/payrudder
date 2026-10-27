from __future__ import annotations

import json
from typing import Any

from payrudder import AsyncPayrudder, Payrudder
from payrudder import models as m
from payrudder._ingest import MAX_BATCH_BYTES, prepare_ingest_batches

from .conftest import FakeGateway, Recorded, Reply


def event(i: int, **extra: Any) -> m.IncomingEvent:
    base: dict[str, Any] = {
        "customer_external_id": f"c{i}",
        "meter_code": "tokens",
        "quantity": "1",
        "event_time": "2026-01-01T00:00:00Z",
    }
    base.update(extra)
    return base  # type: ignore[return-value]


def echo(rec: Recorded) -> Reply:
    """Answer like the gateway: one result per event, `rejected` for customer c3."""
    events = json.loads(rec.body)["events"]
    results = [
        {"status": "rejected", "error_code": "UNKNOWN_CUSTOMER"}
        if e["customer_external_id"] == "c3"
        else {"status": "accepted", "event_id": f"ev-{e['customer_external_id']}"}
        for e in events
    ]
    return Reply(202, {"results": results})


def test_batches_never_exceed_500_events() -> None:
    batches = list(prepare_ingest_batches([event(i) for i in range(1201)]))
    assert [len(b) for b in batches] == [500, 500, 201]


def test_batches_respect_the_byte_limit() -> None:
    big = "x" * 100_000
    batches = list(prepare_ingest_batches([event(i, properties={"blob": big}) for i in range(25)]))
    assert len(batches) > 1
    for batch in batches:
        assert len(json.dumps({"events": batch}, separators=(",", ":")).encode()) <= MAX_BATCH_BYTES
    assert sum(len(b) for b in batches) == 25


def test_generated_keys_are_filled_unique_and_valid_and_the_input_is_untouched() -> None:
    inputs = [event(1), event(2, idempotency_key="mine")]
    batches = list(prepare_ingest_batches(inputs))
    sent = batches[0]
    assert sent[1]["idempotency_key"] == "mine"
    assert sent[0]["idempotency_key"].startswith("sdk-")
    assert "idempotency_key" not in inputs[0]


def test_empty_input_sends_nothing(client: Payrudder, gateway: FakeGateway) -> None:
    assert client.events.ingest([]) == []
    assert gateway.requests == []


def test_results_come_back_in_request_order_and_a_rejection_does_not_raise(
    client: Payrudder, gateway: FakeGateway
) -> None:
    gateway.handler = echo
    events = [event(i) for i in range(1100)]
    results = client.events.ingest(events)
    assert len(gateway.requests) == 3
    assert len(results) == 1100
    assert results[0] == {"status": "accepted", "event_id": "ev-c0"}
    assert results[3] == {"status": "rejected", "error_code": "UNKNOWN_CUSTOMER"}
    assert results[1099]["event_id"] == "ev-c1099"


async def test_async_ingest(aclient: AsyncPayrudder, gateway: FakeGateway) -> None:
    gateway.handler = echo
    results = await aclient.events.ingest([event(i) for i in range(501)])
    assert len(gateway.requests) == 2
    assert [r["status"] for r in results[:5]] == ["accepted", "accepted", "accepted", "rejected", "accepted"]
