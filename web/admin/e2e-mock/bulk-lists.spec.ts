import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

// Bulk actions on the webhook endpoint list (docs/13-admin-ui.md, "Lists"): tick rows, act on all of them, the failure is named and stays ticked.

test.use({ viewport: { width: 1440, height: 900 } });

const ep = (id: string, url: string, active = true) => ({ id, url, description: "", event_types: ["invoice.paid"], active, create_time: "2026-10-20T10:00:00Z" });

test("ticked webhook endpoints are paused together, and one that cannot be changed is named and stays ticked", async ({ page }) => {
  const endpoints = [ep("e1", "https://a.example/hook"), ep("e2", "https://b.example/hook"), ep("e3", "https://c.example/hook", false)];
  const gw = baseTenant(new MockGateway()).on((c) => {
    if (c.path === "/v1/webhook-endpoints" && c.method === "GET") return { body: { endpoints } };
    if (c.method === "PUT" && c.path === "/v1/webhook-endpoints/e2") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } };
    if (c.method === "PUT" && c.path.startsWith("/v1/webhook-endpoints/")) {
      const e = endpoints.find((x) => c.path.endsWith(x.id));
      if (e) e.active = c.body?.active === true;
      return { body: e };
    }
    return undefined;
  });
  await gw.install(page);
  await openTenantPage(page, "/webhooks");
  await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
  await expect(page.getByText("3 selected")).toBeVisible();
  await page.getByTestId("bulk-pause").click();
  // The paused one is skipped, the second is refused, the first is paused.
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 1 not changed");
  await expect(page.getByTestId("bulk-note")).toContainText("https://b.example/hook");
  expect(gw.called("PUT", /\/v1\/webhook-endpoints\//).map((c) => c.path)).toEqual(["/v1/webhook-endpoints/e1", "/v1/webhook-endpoints/e2"]);
  expect(endpoints[0]?.active).toBe(false);
  // The one that failed (and the one that was not an active endpoint) stay ticked.
  await expect(page.getByText("2 selected")).toBeVisible();
});
