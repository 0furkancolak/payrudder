import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { METERS } from "./usage";

// Connect your app: the code is built from the tenant's own meter, the language tabs change it, the address is kept, and the checklist turns green as things exist
// and the first event is counted (the page asks every few seconds while it waits).

test.use({ viewport: { width: 1440, height: 900 } });

const today = new Date().toISOString().slice(0, 10);
function gateway(state: { meters: unknown[]; customers: unknown[]; counted: boolean }) {
  return baseTenant(new MockGateway()).on((c) => {
    if (c.method !== "GET") return undefined;
    if (c.path === "/v1/meters") return { body: { meters: state.meters } };
    if (c.path === "/v1/customers") return { body: { customers: state.customers } };
    if (c.path === "/v1/analytics/usage") {
      return { body: { precision: "exact", quantity_scale: 0, points: state.counted ? [{ start: `${today}T00:00:00Z`, quantity: "3", event_count: "3" }] : [] } };
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
  await expect(page.getByTestId("connect-code-install")).toContainText("npm install @payrudder/sdk");

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
