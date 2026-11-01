import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
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
