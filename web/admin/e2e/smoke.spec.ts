import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { operatorEmail, signInAsOperator } from "./operator";

const gateway = process.env.PAYRUDDER_GATEWAY_URL ?? "http://localhost:42800";
const operatorToken = process.env.PAYRUDDER_OPERATOR_TOKEN ?? "local-operator-token-change-me-0123456789";
const rabbit = process.env.RABBITMQ_URL ?? "http://localhost:42673";
const rabbitAuth = "Basic " + Buffer.from(process.env.RABBITMQ_CREDENTIALS ?? "payrudder:payrudder_local").toString("base64");

type Json = Record<string, unknown>;

async function call(method: string, path: string, token: string, body?: unknown): Promise<{ status: number; body: Json }> {
  const res = await fetch(gateway + path, {
    method,
    headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: res.status, body: (await res.json().catch(() => ({}))) as Json };
}

async function must(method: string, path: string, token: string, body: unknown, want: number): Promise<Json> {
  const r = await call(method, path, token, body);
  expect(r.status, `${method} ${path}: ${JSON.stringify(r.body)}`).toBe(want);
  return r.body;
}

// --- a few lines of protobuf: enough to hand-write one valid SendNotification command for the notifier ---------------------------
const varint = (n: number): number[] => {
  const out: number[] = [];
  while (n > 127) {
    out.push((n & 127) | 128);
    n = Math.floor(n / 128);
  }
  out.push(n);
  return out;
};
const lenField = (num: number, data: Uint8Array | number[]): number[] => [...varint(num * 8 + 2), ...varint(data.length), ...data];
const str = (num: number, s: string): number[] => lenField(num, Buffer.from(s));
const intField = (num: number, v: number): number[] => [...varint(num * 8), ...varint(v)];

/** A CommandEnvelope for the notifier: a receipt to an address, in the sandbox, for a saga that is already final (so the reply is ignored). */
function sendNotification(tenantId: string, sagaId: string): Buffer {
  const entry = (k: string, v: string) => lenField(3, [...str(1, k), ...str(2, v)]);
  const notification = [...str(1, "invoice_paid.v1"), ...str(2, "dlq-smoke@example.com"), ...entry("invoice_number", "SBX-DLQ-SMOKE"), ...entry("amount", "1.00")];
  return Buffer.from([
    ...str(1, randomUUID()), // command_id
    ...str(2, sagaId),
    ...str(3, tenantId),
    ...intField(4, 1), // attempt
    ...intField(6, 1), // ENVIRONMENT_SANDBOX
    ...lenField(14, notification), // send_notification
  ]);
}

// SCREENSHOTS=<dir> makes the smoke test save the views it visits (they illustrate the README).
const shots = process.env.SCREENSHOTS;
async function shot(page: Page, name: string, fullPage = true): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage });
}

async function signIn(page: Page): Promise<void> {
  await signInAsOperator(page);
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
}

test.describe.serial("operator console", () => {
  let tenantId = "";
  let invoiceNumber = "";
  let sagaId = "";

  test.beforeAll(async () => {
    // A sandbox account with one paying customer, one period closed, one invoice through its whole saga.
    const acct = await must("POST", "/admin/v1/accounts", operatorToken, {
      name: `pw-${Date.now()}`, currency: "TRY", timezone: "Europe/Istanbul", grace_period: "86400s", tier: "TENANT_TIER_STANDARD",
    }, 201);
    const tenants = acct.tenants as Array<{ id: string; environment: string }>;
    const sandbox = tenants.find((t) => t.environment === "ENVIRONMENT_SANDBOX");
    expect(sandbox).toBeDefined();
    tenantId = sandbox!.id;
    const key = (await must("POST", `/admin/v1/tenants/${tenantId}/api-keys`, operatorToken, {}, 201)).plaintext as string;
    await must("POST", "/v1/meters", key, { code: "seats", aggregation: "AGGREGATION_SUM", value_property: "n", quantity_scale: 0, unit_label: "seats" }, 201);
    await must("POST", "/v1/plans", key, { code: "team", name: "Team" }, 201);
    await must("POST", "/v1/plans/team/versions", key, { base_fee_minor: 1000, prices: [{ meter_code: "seats", per_unit: { unit_price: "1" } }] }, 201);
    await must("POST", "/v1/customers", key, { external_id: "cus_pw", name: "Playwright", email: "pw@example.com", payment_method: "pm_sandbox_success" }, 201);
    await must("POST", "/v1/subscriptions", key, { customer_external_id: "cus_pw", plan_code: "team", fee_timing: "FEE_TIMING_ARREARS" }, 201);
    await must("POST", "/v1/events", key, {
      events: [{ customer_external_id: "cus_pw", meter_code: "seats", quantity: 5, event_time: new Date(Date.now() - 60_000).toISOString() }],
    }, 202);
    await must("POST", "/v1/sandbox/close-period", key, undefined, 202);

    await expect
      .poll(async () => {
        const r = await call("GET", "/v1/invoices", key);
        const inv = (r.body.invoices as Array<Json> | undefined)?.[0];
        if (inv?.saga_state === "COMPLETED") {
          invoiceNumber = inv.number as string;
          return "COMPLETED";
        }
        return String(inv?.saga_state ?? "none");
      }, { timeout: 240_000, intervals: [2_000] })
      .toBe("COMPLETED");
    const list = await call("GET", `/admin/v1/tenants/${tenantId}/invoices`, operatorToken);
    const invoiceId = (list.body.invoices as Array<Json>)[0]!.id as string;
    const saga = await call("GET", `/admin/v1/tenants/${tenantId}/invoices/${invoiceId}/saga`, operatorToken);
    sagaId = (saga.body.saga as Json).id as string;
  });

  test("an operator user opens an invoice and its saga timeline shows at least five transitions", async ({ page }) => {
    await signIn(page);
    await shot(page, "admin-tenants", false); // the list of a long-running dev stack is long: the first screen is enough
    // The list is paged: search for the tenant instead of hoping it is on the first page.
    await page.getByRole("searchbox").fill(tenantId);
    await page.getByRole("row", { name: new RegExp(tenantId) }).getByRole("link").click();
    await expect(page.getByRole("heading", { name: /.+/ }).first()).toBeVisible();
    await page.getByRole("row", { name: new RegExp(invoiceNumber) }).click();
    await expect(page.getByRole("heading", { name: invoiceNumber })).toBeVisible();
    await expect(page.getByTestId("saga-event").first()).toBeVisible();
    expect(await page.getByTestId("saga-event").count()).toBeGreaterThanOrEqual(5);
    await expect(page.getByTestId("saga-event").last()).toContainText("COMPLETED");
    await shot(page, "admin-invoice-saga");
    await expect(page.getByRole("button", { name: "Open PDF" })).toBeEnabled();
  });

  test("a signed-out visitor is sent to the sign-in page, which has no token form, and the machine token is no password", async ({ page }) => {
    await page.goto("/operator");
    await page.waitForURL("**/signin");
    await page.goto("/tenants");
    await page.waitForURL("**/signin");
    await expect(page.getByLabel(/operator token/i)).toHaveCount(0);
    // The API's machine credential is not a sign-in either: typed as a password it is just a wrong one.
    await page.getByLabel("E-mail").fill(operatorEmail);
    await page.getByLabel("Password", { exact: true }).fill(operatorToken);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert")).toContainText("Wrong e-mail or password");
  });

  test("a person who is not an operator cannot open the operator console, in the page or in the API", async ({ page }) => {
    const email = `plain-${Date.now()}@example.com`;
    const signup = await fetch(`${gateway}/auth/signup`, {
      method: "POST", headers: { "content-type": "application/json" },
      body: JSON.stringify({ email, password: "correct horse battery", account_name: "Plain Co" }),
    });
    expect(signup.status).toBe(202);
    const signin = await call("POST", "/auth/signin", "", { email, password: "correct horse battery" });
    expect(signin.status).toBe(200);
    const session = signin.body.token as string;
    expect(signin.body.operator).toBe(false);
    const refused = await call("GET", "/admin/v1/tenants", session);
    expect(refused.status).toBe(403);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill(email);
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await expect(page.getByRole("link", { name: "Operator console" })).toHaveCount(0);
    await page.goto("/tenants");
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
  });

  test("redriving a dead letter takes it out of the dead-letter view", async ({ page }) => {
    // Put one valid notification command into notification.send.dlq through RabbitMQ's management API.
    const published = await fetch(`${rabbit}/api/exchanges/%2F/amq.default/publish`, {
      method: "POST",
      headers: { authorization: rabbitAuth, "content-type": "application/json" },
      body: JSON.stringify({
        properties: { message_id: randomUUID(), delivery_mode: 2, content_type: "application/x-protobuf" },
        routing_key: "notification.send.dlq",
        payload: sendNotification(tenantId, sagaId).toString("base64"),
        payload_encoding: "base64",
      }),
    });
    expect((await published.json()).routed).toBe(true);

    await signIn(page);
    await page.getByRole("link", { name: "Dead letters" }).click();
    const row = page.getByTestId("dlq-notification.send");
    await expect(row.getByTestId("dlq-count")).not.toHaveText("0");
    await shot(page, "admin-dead-letters");
    page.once("dialog", (d) => d.accept());
    await row.getByRole("button", { name: "Redrive" }).click();
    await expect(page.getByText(/Redriven [1-9]\d* message/)).toBeVisible();
    await expect(row.getByTestId("dlq-count")).toHaveText("0");
  });
});
