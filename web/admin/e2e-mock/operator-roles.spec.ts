import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, operatorUser, TENANT } from "./gateway";

// Plan 14, operator roles: a support operator (PAYRUDDER_OPERATOR_SUPPORT_EMAILS) sees the operator console and cannot change anything in it; the server refuses
// its writes with 403 anyway (gateway tests), so the console does not offer them.

test.use({ viewport: { width: 1440, height: 900 } });

function gateway(role: "operator_admin" | "operator_support" | undefined): MockGateway {
  const gw = baseTenant(new MockGateway());
  if (role) {
    // Answers before the default operator sign-in of the mocks, which carries no role.
    gw.on((c) => {
      if (c.method === "POST" && c.path === "/auth/signin") return { body: { user: operatorUser, memberships: [], token: "qs_operator", operator: true, operator_role: role, expire_time: "2026-11-04T09:00:00Z" } };
      if (c.method === "GET" && c.path === "/auth/me") return { body: { user: operatorUser, memberships: [], operator: true, operator_role: role, expire_time: "2026-11-04T09:00:00Z" } };
      return undefined;
    });
  }
  gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}` ? { body: { id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", tier: "TENANT_TIER_STANDARD", currency: "TRY", timezone: "Europe/Istanbul", status: "TENANT_STATUS_ACTIVE" } } : undefined));
  gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/status-events` ? { body: { events: [] } } : undefined));
  gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/lifecycle` ? { body: {} } : undefined));
  return gw;
}

test.describe("operator roles in the console", () => {
  test("a support operator sees the tenant and its lifecycle but cannot schedule, and the menu names the role", async ({ page }) => {
    const gw = gateway("operator_support");
    await gw.install(page);
    await openTenantPage(page, "");
    const card = page.getByTestId("tenant-lifecycle");
    await expect(card.getByTestId("lifecycle-schedule")).toContainText("Nothing is scheduled.");
    await card.getByTestId("schedule-status").selectOption("frozen");
    await card.getByTestId("schedule-time").fill("2030-01-15T09:30");
    await expect(card.getByTestId("schedule-submit")).toBeDisabled();
    await expect(page.getByTestId("status-apply")).toBeDisabled();
    expect(gw.called("PUT", `/admin/v1/tenants/${TENANT}/scheduled-status`)).toHaveLength(0);
    await page.getByTestId("user-menu").click();
    await expect(page.getByTestId("operator-role")).toContainText("read-only support");
  });

  test("an operator admin keeps the write controls", async ({ page }) => {
    const gw = gateway("operator_admin");
    await gw.install(page);
    await openTenantPage(page, "");
    const card = page.getByTestId("tenant-lifecycle");
    await card.getByTestId("schedule-status").selectOption("frozen");
    await card.getByTestId("schedule-time").fill("2030-01-15T09:30");
    await expect(card.getByTestId("schedule-submit")).toBeEnabled();
  });
});
