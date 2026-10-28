/**
 * The code a developer pastes to connect an app: install the SDK, send a usage event, and verify a webhook, in five languages. Built from the app's own values
 * (the API address, a meter code and a customer id) so the first run works as it stands. The calls are the ones the SDKs' own README files show; nothing here talks
 * to the API.
 */
export type Lang = "curl" | "typescript" | "python" | "go" | "php";
export const LANGS: readonly Lang[] = ["curl", "typescript", "python", "go", "php"];

export interface Inputs {
  /** Where the API is, without a trailing slash. */
  baseUrl: string;
  meter: string;
  customer: string;
  /** qk_test_... for the sandbox, qk_live_... for live: only the shape, never a real key. */
  keyHint: string;
}

export interface Snippets {
  install: string;
  send: string;
  webhook: string;
}

/** A value put inside a quoted string of the generated code: quotes, backslashes and line breaks are escaped so a customer id can never end the string. */
const q = (v: string) => v.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\r?\n/g, " ");
const url = (v: string) => q(v.trim().replace(/\/+$/, ""));

export function snippets(lang: Lang, i: Inputs): Snippets {
  const base = url(i.baseUrl);
  const meter = q(i.meter);
  const cust = q(i.customer);
  const key = q(i.keyHint);
  switch (lang) {
    case "curl": {
      // The JSON sits inside single quotes of the shell: a single quote in a value must close and reopen them, or it would end the string and run what follows.
      const sh = (v: string) => v.replace(/'/g, "'\\''");
      const [cu, me] = [sh(cust), sh(meter)];
      return {
        install: "# Nothing to install: any HTTP client works.\nexport PAYRUDDER_API_KEY=" + key,
        send: `curl -sS ${base}/v1/events \\
  -H "Authorization: Bearer $PAYRUDDER_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"events":[{"customer_external_id":"${cu}","meter_code":"${me}","quantity":"1","event_time":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'","idempotency_key":"first-event-1"}]}'`,
        webhook:
          "# A delivery carries Payrudder-Signature: t=<unix time>,v1=<hex>. The hex is HMAC-SHA256 of \"<t>.<raw body>\" under your endpoint's secret.\n# Check it on the raw body before parsing; the SDKs do it for you (see the other tabs).",
      };
    }
    case "typescript":
      return {
        install: "npm install @payrudder/sdk",
        send: `import { Payrudder } from "@payrudder/sdk";

const payrudder = new Payrudder({ apiKey: process.env.PAYRUDDER_API_KEY, baseUrl: "${base}" }); // ${key}...

await payrudder.customers.createCustomer({ external_id: "${cust}", name: "First customer", email: "first@example.com" });

const results = await payrudder.events.ingest([
  { customer_external_id: "${cust}", meter_code: "${meter}", quantity: "1", event_time: new Date(), idempotency_key: "first-event-1" },
]);
console.log(results[0].status); // accepted | duplicate | rejected`,
        webhook: `import { constructEvent, WebhookVerificationError } from "@payrudder/sdk";

// Express: app.post("/webhooks/payrudder", express.raw({ type: "application/json" }), handler)
async function handler(req, res) {
  try {
    const event = await constructEvent(req.body, req.header("Payrudder-Signature") ?? "", process.env.PAYRUDDER_WEBHOOK_SECRET);
    console.log(event.id, event.type);
    res.sendStatus(200);
  } catch (err) {
    if (err instanceof WebhookVerificationError) return res.sendStatus(400);
    throw err;
  }
}`,
      };
    case "python":
      return {
        install: "pip install ./sdk/python   # from a checkout of the repository",
        send: `from datetime import datetime, timezone
from payrudder import Payrudder

client = Payrudder(api_key="${key}...", base_url="${base}")  # or PAYRUDDER_API_KEY / PAYRUDDER_BASE_URL

client.customers.create_customer({"external_id": "${cust}", "name": "First customer", "email": "first@example.com"})

results = client.events.ingest([
    {"customer_external_id": "${cust}", "meter_code": "${meter}", "quantity": "1",
     "event_time": datetime.now(timezone.utc).isoformat(), "idempotency_key": "first-event-1"},
])
print(results[0]["status"])  # accepted | duplicate | rejected`,
        webhook: `from payrudder import WebhookSignatureError, construct_event

@app.post("/webhooks/payrudder")  # Flask
def hook():
    try:
        event = construct_event(request.get_data(), request.headers.get("Payrudder-Signature"), WEBHOOK_SECRET)
    except WebhookSignatureError:
        return "", 400
    print(event["id"], event["type"])
    return "", 200`,
      };
    case "go":
      return {
        install: "go mod edit -require=github.com/0furkancolak/payrudder/sdk/go@v0.0.0\ngo mod edit -replace=github.com/0furkancolak/payrudder/sdk/go=/path/to/payrudder/sdk/go",
        send: `package main

import (
	"context"
	"fmt"
	"log"
	"time"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

func main() {
	client, err := payrudder.New(payrudder.WithAPIKey("${key}..."), payrudder.WithBaseURL("${base}"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Customers.CreateCustomer(ctx, &payrudder.CreateCustomerRequest{ExternalID: "${cust}", Name: "First customer", Email: "first@example.com"}); err != nil {
		log.Fatal(err)
	}
	results, err := client.Events.Ingest(ctx, []payrudder.IncomingEvent{{
		CustomerExternalID: "${cust}", MeterCode: "${meter}", Quantity: "1", EventTime: time.Now(), IdempotencyKey: "first-event-1",
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(results[0].Status)
}`,
        webhook: `ev, err := payrudder.ConstructEvent(body, r.Header.Get(payrudder.WebhookSignatureHeader), os.Getenv("PAYRUDDER_WEBHOOK_SECRET"), 0)
if err != nil {
	http.Error(w, "bad signature", http.StatusBadRequest)
	return
}
fmt.Println(ev.ID, ev.Type)`,
      };
    case "php":
      return {
        install: 'composer config repositories.payrudder path ../quotify/sdk/php\ncomposer require payrudder/payrudder-php:"*@dev"',
        send: `use Payrudder\\Client;

$client = new Client(apiKey: '${key}...', baseUrl: '${base}');

$client->customers->createCustomer(['external_id' => '${cust}', 'name' => 'First customer', 'email' => 'first@example.com']);

$result = $client->events->ingest([
    ['customer_external_id' => '${cust}', 'meter_code' => '${meter}', 'quantity' => '1', 'event_time' => gmdate('c'), 'idempotency_key' => 'first-event-1'],
]);
var_dump($result[0]->status);`,
        webhook: `use Payrudder\\Webhook;

$payload = file_get_contents('php://input');
try {
    $event = Webhook::constructEvent($payload, $_SERVER['HTTP_PAYRUDDER_SIGNATURE'] ?? '', getenv('PAYRUDDER_WEBHOOK_SECRET'));
} catch (\\Payrudder\\Exception\\WebhookException $e) {
    http_response_code(400);
    exit;
}
echo $event->id;`,
      };
  }
}

/** The shape of the key to expect, from the environment of the tenant the page is for. */
export const keyHintOf = (live: boolean) => (live ? "qk_live_" : "qk_test_");
