// Imports the built ESM and CJS bundles the way a consumer would and exercises the parts that need no network.
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const esm = await import("@payrudder/sdk");
const cjs = createRequire(import.meta.url)("@payrudder/sdk");

for (const [name, mod] of [
  ["esm", esm],
  ["cjs", cjs],
]) {
  assert.equal(typeof mod.Payrudder, "function", `${name}: Payrudder`);
  assert.equal(typeof mod.verifyWebhook, "function", `${name}: verifyWebhook`);
  assert.equal(mod.VERSION, "0.0.0", `${name}: VERSION`);

  const client = new mod.Payrudder({ apiKey: "qk_test_smoke", baseUrl: "http://127.0.0.1:1" });
  assert.equal(typeof client.customers.createCustomer, "function", `${name}: customers.createCustomer`);
  assert.equal(typeof client.events.ingest, "function", `${name}: events.ingest`);

  // A signature made with the documented scheme verifies; a changed body does not.
  const secret = "whsec_smoke";
  const body = '{"id":"1","type":"ping","created":"2026-01-01T00:00:00Z","tenant_id":"t","data":{}}';
  const t = Math.floor(Date.now() / 1000);
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const mac = Buffer.from(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`${t}.${body}`))).toString("hex");
  const header = `t=${t},v1=${mac}`;
  const event = await mod.constructEvent(body, header, secret);
  assert.equal(event.type, "ping", `${name}: constructEvent`);
  await assert.rejects(mod.verifyWebhook(body + " ", header, secret), mod.WebhookVerificationError, `${name}: tampered payload`);
}

console.log("smoke ok: esm + cjs");
