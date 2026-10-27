"""Batching for ``events.ingest``: 1..500 events and at most 1 MiB per request."""

from __future__ import annotations

import json
import uuid
from collections.abc import Iterator, Sequence
from typing import cast

from . import _wire
from .models import IncomingEvent

MAX_EVENTS = 500
# The server limit is 1 MiB of body; keep a little room for the envelope and any proxy overhead.
MAX_BATCH_BYTES = 1024 * 1024 - 1024
_ENVELOPE_BYTES = len('{"events":[]}')


def prepare_ingest_batches(events: Sequence[IncomingEvent]) -> Iterator[list[IncomingEvent]]:
    """Split ``events`` into valid batches, giving every event without one a generated ``idempotency_key``.

    The key is generated here, once, so every automatic retry of a batch re-sends the same keys and the server
    answers ``duplicate`` instead of counting twice. The caller's dicts are not modified.
    """
    batch: list[IncomingEvent] = []
    size = _ENVELOPE_BYTES
    for original in events:
        event = cast("IncomingEvent", dict(original))
        if not event.get("idempotency_key"):
            event["idempotency_key"] = f"sdk-{uuid.uuid4().hex}"
        cost = len(json.dumps(_wire.encode(event, "IncomingEvent"), separators=(",", ":")).encode()) + 1
        if _ENVELOPE_BYTES + cost > MAX_BATCH_BYTES:
            raise ValueError("a single event is larger than the 1 MiB request limit")
        if batch and (len(batch) >= MAX_EVENTS or size + cost > MAX_BATCH_BYTES):
            yield batch
            batch, size = [], _ENVELOPE_BYTES
        batch.append(event)
        size += cost
    if batch:
        yield batch
