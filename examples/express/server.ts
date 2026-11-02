import express from "express";
import { Payrudder, PayrudderError, WebhookVerificationError, constructEvent } from "@payrudder/sdk";

// PAYRUDDER_API_KEY and PAYRUDDER_BASE_URL are read by the SDK; the webhook secret (whsec_...) is the one the endpoint was created with.
const payrudder = new Payrudder();
const webhookSecret = process.env.PAYRUDDER_WEBHOOK_SECRET ?? "";
const port = Number(process.env.PORT ?? 3000);

const app = express();

// The signature covers the exact bytes Payrudder sent, so this route reads the body raw. Do not put a JSON parser in front of it.
app.post("/webhooks/payrudder", express.raw({ type: "application/json" }), async (req, res) => {
  try {
    const event = await constructEvent(req.body, req.header("Payrudder-Signature") ?? "", webhookSecret);
    console.log("webhook", event.id, event.type);
    // Do the work after answering, or put it on a queue: Payrudder retries a delivery that is not answered with a 2xx.
    res.sendStatus(200);
  } catch (err) {
    if (err instanceof WebhookVerificationError) {
      res.status(400).json({ error: err.reason });
      return;
    }
    throw err;
  }
});

// POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"} counts one event for that customer.
app.post("/usage", express.json(), async (req, res) => {
  const { customer, meter, quantity = "1" } = req.body ?? {};
  if (typeof customer !== "string" || typeof meter !== "string") {
    res.status(400).json({ error: "customer and meter are required" });
    return;
  }
  try {
    // A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
    const key = req.header("Idempotency-Key") ?? crypto.randomUUID();
    const [result] = await payrudder.events.ingest([{ customer_external_id: customer, meter_code: meter, quantity: String(quantity), event_time: new Date(), idempotency_key: key }]);
    res.json(result);
  } catch (err) {
    if (err instanceof PayrudderError) {
      res.status(502).json({ error: err.key ?? err.message });
      return;
    }
    throw err;
  }
});

app.get("/healthz", (_req, res) => {
  res.send("ok");
});

app.listen(port, () => console.log(`listening on :${port}`));
