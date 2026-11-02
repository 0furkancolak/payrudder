import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { APP, LIVE, productGateway, SANDBOX, signInAsAda, type ProductState } from "./product";
import { METERS } from "./usage";

// Connect your app: the code is built from the tenant's own meter, the language tabs change it, the address is kept, and the checklist turns green as things exist
// and the first event is counted (the page asks every few seconds while it waits).

test.use({ viewport: { width: 1440, height: 900 } });

function gateway(state: { meters: unknown[]; customers: unknown[]; counted: boolean }) {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/meters") return { body: { meters: state.meters } };
    if (c.path === "/v1/customers") return { body: { customers: state.customers } };
    if (c.path === "/v1/analytics/usage") {
      // The console's clock is not the test's (a fixture may fix it), so the event is put on the last day of the range that was asked for.
      const last = new Date(Date.parse(c.query.get("to") ?? "") - 86_400_000).toISOString().slice(0, 10);
      return { body: { precision: "exact", quantity_scale: 0, points: state.counted ? [{ start: `${last}T00:00:00Z`, quantity: "3", event_count: "3" }] : [] } };
    }
    return undefined;
  });
}

test("the code uses the tenant's meter and the address typed, in every language, and the address is kept", async ({ page }) => {
  const gw = gateway({ meters: METERS, customers: [], counted: false });
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await expect(page.getByTestId("connect-meter")).toHaveValue("api_calls");
  await page.getByTestId("connect-address").fill("https://api.shop.example/");
  await page.getByTestId("connect-customer").fill("cus_shop_1");

  const send = page.getByTestId("connect-code-send");
  await expect(send).toContainText(`meter_code: "api_calls"`);
  await expect(send).toContainText("https://api.shop.example");
  await expect(send).toContainText("cus_shop_1");
  await expect(page.getByTestId("connect-code-install")).toContainText("payrudder-sdk-0.0.0.tgz");

  await page.getByTestId("connect-lang").getByText("Python", { exact: true }).click();
  await expect(send).toContainText('"meter_code": "api_calls"');
  await expect(page.getByTestId("connect-code-webhook")).toContainText("construct_event");
  await page.getByTestId("connect-lang").getByText("curl", { exact: true }).click();
  await expect(send).toContainText("https://api.shop.example/v1/events");
  await expect(page.getByTestId("connect-code-webhook")).toContainText("Payrudder-Signature");

  // The address survives a reload.
  await page.reload();
  await expect(page.getByTestId("connect-address")).toHaveValue("https://api.shop.example/");
  // A sandbox tenant is told to use a test key.
  await expect(page.getByText("qk_test_").first()).toBeVisible();
});

test("the address starts as the deployment's own setting (/env.js), and one the person typed wins", async ({ page }) => {
  await page.route("**/env.js", (route) =>
    route.fulfill({ contentType: "application/javascript", body: 'window.__PAYRUDDER__={"prefill":false,"brand":"","demo":false,"api":"https://api.deploy.example"};' }),
  );
  const gw = gateway({ meters: METERS, customers: [], counted: false });
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await expect(page.getByTestId("connect-address")).toHaveValue("https://api.deploy.example");
  await expect(page.getByTestId("connect-code-send")).toContainText("https://api.deploy.example");
  await page.getByTestId("connect-address").fill("https://mine.example");
  await page.reload();
  await expect(page.getByTestId("connect-address")).toHaveValue("https://mine.example");
});

test("the checklist turns green as the meter, the customer and the first event appear", async ({ page }) => {
  const state = { meters: [] as unknown[], customers: [] as unknown[], counted: false };
  const gw = gateway(state);
  await gw.install(page);
  await openTenantPage(page, "/connect");
  for (const k of ["meter", "customer", "event"]) await expect(page.getByTestId(`connect-step-${k}`)).toHaveAttribute("data-done", "false");
  await expect(page.getByTestId("connect-waiting")).toBeVisible();

  state.meters = METERS;
  state.customers = [{ id: "c1", external_id: "cus_1", name: "A", email: "a@example.com" }];
  await page.reload();
  await expect(page.getByTestId("connect-step-meter")).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("connect-step-customer")).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("connect-step-event")).toHaveAttribute("data-done", "false");

  // The first event is counted while the page waits: it turns green by itself.
  state.counted = true;
  await expect(page.getByTestId("connect-step-event")).toHaveAttribute("data-done", "true", { timeout: 15_000 });
  await expect(page.getByTestId("connect-waiting")).toHaveCount(0);
});

// "Send a test webhook": the button posts to the chosen endpoint, then the page follows that delivery (asked for every few seconds) until the endpoint has answered.
const ENDPOINTS = [
  { id: "e1", url: "https://hooks.example/one", description: "", event_types: [], active: true },
  { id: "e2", url: "https://hooks.example/two", description: "", event_types: [], active: true },
];
type Delivery = { state: string; attempts: number; last_status: number; last_error: string };
const PENDING: Delivery = { state: "WEBHOOK_DELIVERY_STATE_PENDING", attempts: 0, last_status: 0, last_error: "" };

function webhookGateway(delivery: { now: Delivery }, endpoints: unknown[] = ENDPOINTS) {
  const row = () => ({ id: "d1", event_id: "ev1", event_type: "ping", endpoint_id: "e2", next_attempt_time: null, create_time: "2026-10-21T12:00:00Z", delivered_time: null, ...delivery.now });
  return gateway({ meters: METERS, customers: [], counted: false }).on((c) => {
    if (c.method === "GET" && c.path === "/v1/webhook-endpoints") return { body: { endpoints } };
    if (c.method === "POST" && c.path.endsWith("/test")) return { status: 202, body: row() };
    if (c.method === "GET" && c.path === "/v1/webhook-deliveries") return { body: { deliveries: [row()], next_page_token: "" } };
    return undefined;
  });
}

test("a test webhook goes to the chosen endpoint and the page shows that the server accepted it", async ({ page }) => {
  const delivery = { now: PENDING };
  const gw = webhookGateway(delivery);
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await expect(page.getByTestId("connect-step-webhook")).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("connect-webhook-result")).toHaveCount(0);

  await page.getByTestId("connect-webhook-endpoint").selectOption("e2");
  await page.getByTestId("connect-webhook-send").click();
  await expect(page.getByTestId("connect-webhook-result")).toHaveAttribute("data-outcome", "queued");
  expect(gw.called("POST", "/v1/webhook-endpoints/e2/test")).toHaveLength(1);
  expect(gw.called("POST", "/v1/webhook-endpoints/e1/test")).toHaveLength(0);

  delivery.now = { state: "WEBHOOK_DELIVERY_STATE_DELIVERED", attempts: 1, last_status: 200, last_error: "" };
  await expect(page.getByTestId("connect-webhook-result")).toHaveAttribute("data-outcome", "delivered", { timeout: 15_000 });
  await expect(page.getByTestId("connect-webhook-result")).toContainText("HTTP 200");
});

test("a test webhook that the server refuses shows its answer", async ({ page }) => {
  const delivery = { now: { state: "WEBHOOK_DELIVERY_STATE_PENDING", attempts: 1, last_status: 400, last_error: "HTTP 400: <html><body>bad signature</body></html>" } };
  const gw = webhookGateway(delivery);
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await page.getByTestId("connect-webhook-send").click();
  const result = page.getByTestId("connect-webhook-result");
  await expect(result).toHaveAttribute("data-outcome", "failed");
  await expect(result).toContainText("HTTP 400: bad signature");
});

test("without an endpoint the page says to add one and offers no button", async ({ page }) => {
  const gw = webhookGateway({ now: PENDING }, []);
  await gw.install(page);
  await openTenantPage(page, "/connect");
  await expect(page.getByTestId("connect-step-webhook")).toHaveAttribute("data-done", "false");
  await expect(page.getByTestId("connect-webhook-none")).toBeVisible();
  await expect(page.getByTestId("connect-webhook-send")).toHaveCount(0);
});

// The first things made from the page itself: an ingest-only key (shown once, kept nowhere), in the sandbox a sample meter and customer, and a test event sent as the person.
// A person who owns the app (Ada) is offered all three; an operator looking at a tenant has no app of their own here, so no key.
type Tenantish = { meters: unknown[]; customers: unknown[]; events: Record<string, unknown>[]; eventResult: Record<string, unknown> };

function ownerGateway(state: Tenantish, keysState: Partial<ProductState> = {}) {
  const { gw, state: product } = productGateway(new MockGateway(), keysState);
  gw.on((c) => {
    if (c.path === "/v1/meters") {
      if (c.method === "POST") {
        state.meters = [{ ...(c.body as object), version: 1 }];
        return { status: 201, body: state.meters[0] };
      }
      return { body: { meters: state.meters } };
    }
    if (c.path === "/v1/customers") {
      if (c.method === "POST") {
        const made = { id: "c1", name: "", email: "", ...(c.body as object) };
        state.customers = [made];
        return { status: 201, body: made };
      }
      return { body: { customers: state.customers, next_page_token: "" } };
    }
    if (c.method === "POST" && c.path === "/v1/events") {
      state.events.push(c.body as Record<string, unknown>);
      return { status: 202, body: { results: [state.eventResult] } };
    }
    if (c.method === "GET" && c.path === "/v1/analytics/usage") return { body: { precision: "exact", quantity_scale: 0, points: [] } };
    if (c.method === "GET" && c.path === "/v1/webhook-endpoints") return { body: { endpoints: [] } };
    return undefined;
  });
  return { gw, product };
}
const fresh = (): Tenantish => ({ meters: [], customers: [], events: [], eventResult: { status: "accepted", event_id: "ev-1" } });

test("an owner makes an ingest-only key on the page: shown once, copied, never stored", async ({ page }) => {
  const { gw } = ownerGateway(fresh(), { keys: [] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  await expect(page.getByTestId("connect-step-key")).toHaveAttribute("data-done", "false");
  await expect(page.getByTestId("connect-key-secret")).toHaveCount(0);

  await page.getByTestId("connect-key-create").click();
  // The sandbox key of the sandbox environment, for the least it needs: sending events.
  expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "sandbox", role: "ingest_only" });
  const secret = page.getByTestId("connect-key-secret");
  await expect(secret).toContainText("qk_test_newkey99_SECRETSECRETSECRETSECRET");
  await expect(page.getByTestId("connect-step-key")).toHaveAttribute("data-done", "true");
  // The code on the page never holds it (it reads the key from the environment).
  await expect(page.getByTestId("connect-code-send")).not.toContainText("SECRETSECRET");
  // Nothing about it is kept in the browser.
  const kept = await page.evaluate(() => JSON.stringify([{ ...localStorage }, { ...sessionStorage }]));
  expect(kept).not.toContain("SECRETSECRET");

  await page.getByTestId("connect-key-done").click();
  await expect(page.getByTestId("connect-key-secret")).toHaveCount(0);
  await expect(page.getByTestId("connect-key-create")).toBeVisible();
  await page.reload();
  await expect(page.getByTestId("connect-key-secret")).toHaveCount(0);
  // The key exists, so the step stays green without its secret.
  await expect(page.getByTestId("connect-step-key")).toHaveAttribute("data-done", "true");
});

test("a refused key shows why, and an operator looking at a tenant is not offered one", async ({ page }) => {
  const refuse = { status: 403, body: { error: { code: "PERMISSION_DENIED", key: "identity.email.not_verified", message: "confirm your address" } } };
  const { gw } = ownerGateway(fresh());
  gw.on((c) => (c.method === "POST" && c.path === `/app/v1/apps/${APP}/api-keys` ? refuse : undefined));
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/connect`);
  await page.getByTestId("connect-key-create").click();
  expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "live", role: "ingest_only" });
  await expect(page.getByTestId("connect-key")).toContainText(/verified|confirm/i);
  await expect(page.getByTestId("connect-key-secret")).toHaveCount(0);

  const operator = gateway({ meters: METERS, customers: [], counted: false });
  const op = await page.context().newPage();
  await operator.install(op);
  await openTenantPage(op, "/connect");
  await expect(op.getByTestId("connect-meter")).toHaveValue("api_calls");
  await expect(op.getByTestId("connect-key")).toHaveCount(0);
});

test("one click makes the sample meter and customer in the sandbox, and the checklist and the code follow", async ({ page }) => {
  const state = fresh();
  const { gw } = ownerGateway(state);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  for (const k of ["meter", "customer"]) await expect(page.getByTestId(`connect-step-${k}`)).toHaveAttribute("data-done", "false");

  await page.getByTestId("connect-sample-create").click();
  await expect(page.getByTestId("connect-step-meter")).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("connect-step-customer")).toHaveAttribute("data-done", "true");
  expect(gw.called("POST", "/v1/meters")[0]?.body).toMatchObject({ code: "api_calls", aggregation: "AGGREGATION_SUM", quantity_scale: 0 });
  expect(gw.called("POST", "/v1/customers")[0]?.body).toMatchObject({ external_id: "cus_1", payment_method: "pm_sandbox_success" });
  await expect(page.getByTestId("connect-meter")).toHaveValue("api_calls");
  await expect(page.getByTestId("connect-code-send")).toContainText("cus_1");
  // Nothing is missing any more: the button goes away.
  await expect(page.getByTestId("connect-sample")).toHaveCount(0);
});

test("a sample is made only for what is missing, and never in live", async ({ page }) => {
  const state = fresh();
  state.meters = METERS;
  const { gw } = ownerGateway(state);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  await page.getByTestId("connect-sample-create").click();
  await expect(page.getByTestId("connect-step-customer")).toHaveAttribute("data-done", "true");
  expect(gw.called("POST", "/v1/meters")).toHaveLength(0);
  expect(gw.called("POST", "/v1/customers")).toHaveLength(1);

  await page.goto(`/app/t/${LIVE}/connect`);
  await expect(page.getByTestId("connect-step-meter")).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("connect-sample")).toHaveCount(0);
  await expect(page.getByTestId("connect-test-event")).toHaveCount(0);
});

test("a test event goes out as the person, for the chosen meter and customer, and its result is shown", async ({ page }) => {
  const state = fresh();
  state.meters = METERS;
  state.customers = [{ id: "c1", external_id: "cus_1", name: "A", email: "a@example.com" }];
  const { gw } = ownerGateway(state);
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  await page.getByTestId("connect-customer").fill("cus_shop_7");
  await page.getByTestId("connect-test-event-send").click();
  const result = page.getByTestId("connect-test-event-result");
  await expect(result).toHaveAttribute("data-outcome", "accepted");
  const sent = gw.called("POST", "/v1/events")[0];
  const event = (sent?.body?.events as Record<string, unknown>[])[0];
  expect(event).toMatchObject({ customer_external_id: "cus_shop_7", meter_code: "api_calls", quantity: "1" });
  expect(String(event?.idempotency_key)).toMatch(/^console-test-\d+$/);
  // The console's clock is the page's, which a fixture may have fixed: the event is "now" by that clock.
  expect(Math.abs(Date.parse(String(event?.event_time)) - (await page.evaluate(() => Date.now())))).toBeLessThan(120_000);
  expect(sent?.headers["x-payrudder-tenant"]).toBe(SANDBOX);

  // Changing what is sent clears the old result; a rejected event shows its code and the API's words.
  await page.getByTestId("connect-customer").fill("nobody");
  await expect(result).toHaveCount(0);
  state.eventResult = { status: "rejected", error_code: "UNKNOWN_CUSTOMER", error_message: "no customer nobody" };
  await page.getByTestId("connect-test-event-send").click();
  await expect(result).toHaveAttribute("data-outcome", "rejected");
  await expect(result).toContainText("UNKNOWN_CUSTOMER");
  await expect(result).toContainText("no customer nobody");
  // The hint says what to do, in the console's language.
  await expect(page.getByTestId("connect-test-event-hint")).toContainText("Create the customer first");
});

test("the test event button waits for a meter and a customer id", async ({ page }) => {
  const { gw } = ownerGateway(fresh());
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  await expect(page.getByTestId("connect-test-event-send")).toBeDisabled();
  await expect(page.getByTestId("connect-test-event")).toContainText("Choose a meter");
});

test("with a new key, the sample button and the test event on screen, the page does not scroll sideways on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  const { gw } = ownerGateway(fresh(), { keys: [] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${SANDBOX}/connect`);
  await page.getByTestId("connect-key-create").click();
  await expect(page.getByTestId("connect-key-secret")).toBeVisible();
  await expect(page.getByTestId("connect-sample-create")).toBeVisible();
  await expect(page.getByTestId("connect-test-event")).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
