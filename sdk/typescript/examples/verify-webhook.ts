import { createServer } from "node:http";
import { constructEvent, WebhookVerificationError } from "@payrudder/sdk";

const secret = process.env.PAYRUDDER_WEBHOOK_SECRET;
if (!secret) throw new Error("set PAYRUDDER_WEBHOOK_SECRET to the secret of your webhook endpoint");

createServer((req, res) => {
  void (async () => {
    // The signature is over the raw bytes: read the body as it arrived, parse it only after it verified.
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(chunk as Buffer);
    try {
      const event = await constructEvent(Buffer.concat(chunks), String(req.headers["payrudder-signature"] ?? ""), secret);
      // Deliveries are at least once: ignore an event id you have already handled.
      console.log(event.id, event.type);
      res.writeHead(200).end();
    } catch (err) {
      if (!(err instanceof WebhookVerificationError)) throw err;
      res.writeHead(400).end(); // not signed by this endpoint's secret, or too old
    }
  })();
}).listen(3000, () => console.log("listening on http://localhost:3000/"));
