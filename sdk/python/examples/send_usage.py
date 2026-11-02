"""Send one usage event.

PAYRUDDER_API_KEY (qk_test_... for the sandbox) and PAYRUDDER_BASE_URL are read from the environment.
"""

import sys
from datetime import datetime, timezone

from payrudder import Payrudder

client = Payrudder()

results = client.events.ingest(
    [
        {
            "customer_external_id": "cus_42",
            "meter_code": "api_calls",
            "quantity": "1",
            "event_time": datetime.now(timezone.utc),
            # Name the thing that happened (a request id, a job id) so a retry never counts twice.
            # A live key must send one.
            "idempotency_key": "request-8f2c",
        }
    ]
)
result = results[0]
if result["status"] == "rejected":
    sys.exit(f"rejected: {result.get('error_code')}: {result.get('error_message')}")
print(result["status"], result.get("event_id"))  # accepted | duplicate
