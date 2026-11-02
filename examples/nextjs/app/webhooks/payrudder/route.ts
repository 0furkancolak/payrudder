import { WebhookVerificationError, constructEvent } from "@payrudder/sdk";

// PAYRUDDER_WEBHOOK_SECRET (whsec_...) is the secret the endpoint was created with.
export async function POST(request: Request): Promise<Response> {
  // The signature covers the exact bytes Payrudder sent: read them raw, never request.json().
  const payload = await request.arrayBuffer();
  try {
    const event = await constructEvent(payload, request.headers.get("Payrudder-Signature") ?? "", process.env.PAYRUDDER_WEBHOOK_SECRET ?? "");
    console.log("webhook", event.id, event.type);
    // Do the work after answering, or put it on a queue: Payrudder retries a delivery that is not answered with a 2xx.
    return new Response(null, { status: 200 });
  } catch (err) {
    if (err instanceof WebhookVerificationError) return Response.json({ error: err.reason }, { status: 400 });
    throw err;
  }
}
