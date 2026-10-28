import { expect, test } from "@playwright/test";
import { MockGateway, signInAsOperator } from "./gateway";
import { confirmDialog, rowAction } from "./ui";

// The operator suspends and reinstates tenants from the list, one or many at once; only an active tenant can be suspended and only a suspended one reinstated.

const base = { account_id: "a", app_id: "p", environment: "ENVIRONMENT_LIVE", tier: "TENANT_TIER_STANDARD", currency: "EUR", timezone: "UTC", create_time: "2026-10-01T00:00:00Z" };
const tenants = [
  { ...base, id: "t1", name: "Alpha", status: "TENANT_STATUS_ACTIVE" },
  { ...base, id: "t2", name: "Beta", status: "TENANT_STATUS_ACTIVE" },
  { ...base, id: "t3", name: "Gamma", status: "TENANT_STATUS_INACTIVE" },
];

test.use({ viewport: { width: 1440, height: 900 } });

test("a tenant is suspended after asking and reinstated; a bulk change names the one that failed", async ({ page }) => {
  const gw = new MockGateway();
  gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants } } : undefined));
  gw.on((c) => {
    const m = /^\/admin\/v1\/tenants\/(t\d)\/status$/.exec(c.path);
    if (!m || c.method !== "PUT") return undefined;
    if (m[1] === "t2" && c.body?.status === "suspended") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "plans.request.invalid", message: "no" } } };
    const t = tenants.find((x) => x.id === m[1])!;
    t.status = c.body?.status === "suspended" ? "TENANT_STATUS_SUSPENDED" : "TENANT_STATUS_ACTIVE";
    return { body: { id: t.id, status: t.status } };
  });
  await gw.install(page);
  await signInAsOperator(page);
  await expect(page.getByText("Alpha")).toBeVisible();

  // An inactive tenant offers neither action.
  await expect(page.getByRole("row", { name: /Gamma/ }).getByTestId("row-actions")).toHaveCount(0);

  await rowAction(page.getByRole("row", { name: /Alpha/ }), "Suspend");
  await confirmDialog(page);
  await expect(page.getByRole("row", { name: /Alpha/ }).getByTestId("tenant-suspended")).toBeVisible();
  expect(gw.called("PUT", "/admin/v1/tenants/t1/status")[0]?.body).toMatchObject({ status: "suspended" });

  await rowAction(page.getByRole("row", { name: /Alpha/ }), "Reinstate");
  await expect(page.getByRole("row", { name: /Alpha/ }).getByTestId("tenant-suspended")).toHaveCount(0);

  // Bulk: Beta is refused, Alpha is suspended, the inactive Gamma is skipped, and Beta is named.
  for (const name of ["Alpha", "Beta", "Gamma"]) await page.getByRole("row", { name: new RegExp(name) }).getByTestId("row-check").click();
  await page.getByTestId("bulk-suspend").click();
  await confirmDialog(page);
  await expect(page.getByTestId("bulk-note")).toContainText("1 done, 1 not changed");
  await expect(page.getByTestId("bulk-note")).toContainText("Beta");
  expect(gw.called("PUT", "/admin/v1/tenants/t3/status")).toHaveLength(0);
});
