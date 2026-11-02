import os
import uuid
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import datetime, timezone

from fastapi import FastAPI, Header, HTTPException, Request
from pydantic import BaseModel

from payrudder import APIError, AsyncPayrudder, WebhookSignatureError, construct_event

# PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL are read by the SDK; the webhook secret (whsec_...) is the one the endpoint was created with.
WEBHOOK_SECRET = os.environ.get("PAYRUDDER_WEBHOOK_SECRET", "")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    app.state.payrudder = AsyncPayrudder()
    yield
    await app.state.payrudder.aclose()


app = FastAPI(lifespan=lifespan)


@app.post("/webhooks/payrudder")
async def webhook(request: Request) -> dict[str, str]:
    # The signature covers the exact bytes Payrudder sent: read them raw, never re-serialise a parsed body.
    payload = await request.body()
    try:
        event = construct_event(payload, request.headers.get("Payrudder-Signature"), WEBHOOK_SECRET)
    except WebhookSignatureError as err:
        raise HTTPException(status_code=400, detail=str(err)) from err
    print("webhook", event["id"], event["type"])
    # Do the work after answering, or put it on a queue: Payrudder retries a delivery that is not answered with a 2xx.
    return {"id": event["id"]}


class Usage(BaseModel):
    customer: str
    meter: str
    quantity: str = "1"


@app.post("/usage")
async def usage(body: Usage, request: Request, idempotency_key: str | None = Header(default=None)) -> object:
    """Counts one usage event for a customer."""
    # A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
    key = idempotency_key or str(uuid.uuid4())
    try:
        results = await request.app.state.payrudder.events.ingest(
            [
                {
                    "customer_external_id": body.customer,
                    "meter_code": body.meter,
                    "quantity": body.quantity,
                    "event_time": datetime.now(timezone.utc).isoformat(),
                    "idempotency_key": key,
                }
            ]
        )
    except APIError as err:
        raise HTTPException(status_code=502, detail=err.key or err.message) from err
    return results[0]


@app.get("/healthz")
async def healthz() -> str:
    return "ok"
