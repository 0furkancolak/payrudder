import functools
import json
import logging
import os
import uuid
from datetime import datetime, timezone

from django.http import HttpRequest, HttpResponse, JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_GET, require_POST

from payrudder import APIError, Payrudder, WebhookSignatureError, construct_event

log = logging.getLogger(__name__)

# PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL are read by the SDK; the webhook secret (whsec_...) is the one the endpoint was created with.
WEBHOOK_SECRET = os.environ.get("PAYRUDDER_WEBHOOK_SECRET", "")


@functools.cache
def client() -> Payrudder:
    # Made on first use, so the app starts (and `manage.py check` runs) without the settings.
    return Payrudder()


@csrf_exempt
@require_POST
def webhook(request: HttpRequest) -> JsonResponse:
    # The signature covers the exact bytes Payrudder sent: request.body is the raw body, never json.dumps of a parsed one.
    try:
        event = construct_event(request.body, request.headers.get("Payrudder-Signature"), WEBHOOK_SECRET)
    except WebhookSignatureError as err:
        return JsonResponse({"error": str(err)}, status=400)
    log.info("webhook %s %s", event["id"], event["type"])
    # Do the work after answering (a task queue), or the delivery may time out: Payrudder retries one that is not answered with a 2xx.
    return JsonResponse({"id": event["id"]})


@csrf_exempt
@require_POST
def usage(request: HttpRequest) -> JsonResponse:
    """POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"} counts one event for that customer."""
    try:
        body = json.loads(request.body)
    except ValueError:
        body = None
    if not isinstance(body, dict) or not isinstance(body.get("customer"), str) or not isinstance(body.get("meter"), str):
        return JsonResponse({"error": "customer and meter are required"}, status=400)
    # A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
    key = request.headers.get("Idempotency-Key") or str(uuid.uuid4())
    try:
        results = client().events.ingest(
            [
                {
                    "customer_external_id": body["customer"],
                    "meter_code": body["meter"],
                    "quantity": str(body.get("quantity", "1")),
                    "event_time": datetime.now(timezone.utc).isoformat(),
                    "idempotency_key": key,
                }
            ]
        )
    except APIError as err:
        return JsonResponse({"error": err.key or err.message}, status=502)
    return JsonResponse(results[0])


@require_GET
def healthz(request: HttpRequest) -> HttpResponse:
    return HttpResponse("ok", content_type="text/plain")
