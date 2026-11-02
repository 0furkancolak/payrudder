// Smoke test of a framework template: node examples/smoke.mjs <example directory> <command> [args...]   (the command is run in that directory)
// It starts the app next to docs-site/fakeapi.mjs (a stand-in for the API made from api/openapi/v1.yaml), then checks over HTTP what a developer relies on:
//   - a delivery signed the documented way (Payrudder-Signature: t=<unix>,v1=<HMAC-SHA256 of "<t>.<raw body>">) is answered 200;
//   - the same delivery with a changed body, a wrong secret, an old timestamp or no header is answered 400;
//   - POST /usage reaches the API as a usage event that fits the contract, with the app's key, and the API's answer comes back.
// It proves the template runs and uses the SDK correctly; it does not prove anything about a real gateway. Needs Node 22.18+ and `npm ci` in docs-site.
import { spawn } from "node:child_process";
import { createHmac } from "node:crypto";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { loadSpec, startFakeApi } from "../docs-site/fakeapi.mjs";

const [dir, cmd, ...args] = process.argv.slice(2);
if (!dir || !cmd) {
  console.error("usage: node examples/smoke.mjs <example directory> <command> [args...]");
  process.exit(2);
}

const freePort = () =>
  new Promise((ok, fail) => {
    const s = createServer();
    s.once("error", fail);
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });

const SECRET = "whsec_smoke_secret";
const KEY = "qk_test_smoke";
const api = await startFakeApi(await loadSpec());
const port = await freePort();
const app = spawn(cmd, args, {
  cwd: resolve(dir),
  detached: true, // its own process group, so `go run` or a reloader is stopped with it
  env: { ...process.env, PORT: String(port), PAYRUDDER_API_KEY: KEY, PAYRUDDER_BASE_URL: api.url, PAYRUDDER_WEBHOOK_SECRET: SECRET },
});
let log = "";
for (const s of [app.stdout, app.stderr]) s.on("data", (c) => (log += c));
let exited = null;
app.on("exit", (code) => (exited = code ?? "signal"));

const base = `http://127.0.0.1:${port}`;
const sign = (body, secret = SECRET, t = Math.floor(Date.now() / 1000)) => `t=${t},v1=${createHmac("sha256", secret).update(`${t}.${body}`).digest("hex")}`;
const post = (path, body, headers = {}) => fetch(base + path, { method: "POST", body, headers: { "content-type": "application/json", ...headers } });

let failed = "";
const expect = (what, got, want) => {
  if (got !== want && !failed) failed = `${what}: got ${got}, wanted ${want}`;
};

try {
  for (let i = 0; i < 300; i++) {
    if (exited !== null) throw new Error(`the app exited (${exited}) before it listened`);
    if (await fetch(`${base}/healthz`).then((r) => r.ok, () => false)) break;
    await new Promise((r) => setTimeout(r, 100));
    if (i === 299) throw new Error("the app did not answer /healthz in 30 s");
  }

  const body = JSON.stringify({ id: "0192f0b4-0000-7000-8000-000000000001", type: "ping", created: "2026-11-03T10:15:00Z", tenant_id: "0192f0b4-0000-7000-8000-000000000002", data: {} });
  const sig = (h) => ({ "Payrudder-Signature": h });
  expect("a correctly signed delivery", (await post("/webhooks/payrudder", body, sig(sign(body)))).status, 200);
  expect("a changed body", (await post("/webhooks/payrudder", body.replace("ping", "pong"), sig(sign(body)))).status, 400);
  expect("a wrong secret", (await post("/webhooks/payrudder", body, sig(sign(body, "whsec_other")))).status, 400);
  expect("a timestamp ten minutes old", (await post("/webhooks/payrudder", body, sig(sign(body, SECRET, Math.floor(Date.now() / 1000) - 600)))).status, 400);
  expect("no signature header", (await post("/webhooks/payrudder", body)).status, 400);

  const used = await post("/usage", JSON.stringify({ customer: "cus_42", meter: "api_calls" }), { "Idempotency-Key": "smoke-1" });
  expect("POST /usage", used.status, 200);
  expect("the API's answer is returned", (await used.json()).status, "accepted");
  // 400 or 422 (FastAPI's word for a body that does not fit): a refusal of the caller, not a call to the API.
  expect("POST /usage without a customer is refused as the caller's mistake", [400, 422].includes((await post("/usage", JSON.stringify({ meter: "api_calls" }))).status), true);

  const sent = api.seen.filter((r) => r.path === "/v1/events");
  expect("usage events that reached the API (the refused one must not)", sent.length, 1);
  const r = sent[0];
  if (r && !failed) {
    expect("the key the API saw", r.auth, `Bearer ${KEY}`);
    expect("what the contract objects to", r.problems.join("; "), "");
    expect("the customer", r.body?.events?.[0]?.customer_external_id, "cus_42");
    expect("the meter", r.body?.events?.[0]?.meter_code, "api_calls");
    expect("the idempotency key", r.body?.events?.[0]?.idempotency_key, "smoke-1");
  }
} catch (err) {
  failed = failed || String(err.message ?? err);
} finally {
  try {
    process.kill(-app.pid, "SIGTERM");
  } catch {
    /* it was gone already */
  }
  await api.close();
}

if (failed) {
  console.error(`FAIL ${dir}: ${failed}\n--- the app's output ---\n${log}`);
  process.exit(1);
}
console.log(`ok ${dir}: signed delivery accepted, four bad ones refused, usage reached the API as a valid event`);
process.exit(0);
