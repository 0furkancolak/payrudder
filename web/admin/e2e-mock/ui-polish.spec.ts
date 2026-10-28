import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { productGateway } from "./product";
import { METERS, usageReads } from "./usage";

// Findings UI-10, UI-13, UI-14, UI-15, UI-29 of docs/code-quality/ui-test-notes.md: a few things the console got wrong in small ways.

const sub = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  customer_external_id: "cus_1",
  plan_code: "basic",
  plan_version: 1,
  state: "SUBSCRIPTION_STATE_ACTIVE",
  currency: "TRY",
  owner: "payrudder",
  fee_timing: "FEE_TIMING_ADVANCE",
  interval: "SUBSCRIPTION_INTERVAL_MONTH",
  start_time: "2026-10-01T00:00:00Z",
  ...extra,
});

const subscriptionsTenant = (subs: Record<string, unknown>[]) =>
  baseTenant(new MockGateway()).on((c) => {
    if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: subs } };
    if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Name cus_1", email: "cus_1@example.com" }] } };
    if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "basic", name: "Basic", latest_version: 1 }, { id: "p2", code: "pro", name: "Pro", latest_version: 1 }] } };
    if (c.path.startsWith("/v1/plans/") && c.path.endsWith("/versions/latest")) return { body: { version: 1, base_fee_minor: "1000", publish_time: "2026-10-05T10:00:00Z" } };
    return undefined;
  });

test.describe("a change of plan (UI-13)", () => {
  const ended = sub("s1", { state: "SUBSCRIPTION_STATE_CANCELLED", end_time: "2026-10-10T00:00:00Z" });
  const started = sub("s2", { plan_code: "pro", changed_from_id: "s1", start_time: "2026-10-10T00:00:00Z" });

  test("the cancelled subscription points to the one that took over, and back", async ({ page }) => {
    const gw = subscriptionsTenant([ended, started]);
    await gw.install(page);
    await openTenantPage(page, `/subscriptions?sheet=${encodeURIComponent("subscription:s1")}`);
    const trail = page.getByTestId("change-trail");
    await expect(trail).toContainText("pro v1");
    await trail.getByTestId("trail-next").click();
    await expect(page).toHaveURL(/subscription%3As2|subscription:s2/);
    await expect(page.getByTestId("change-trail")).toContainText("Started by a plan change from basic v1");
    await page.getByTestId("trail-previous").click();
    await expect(page).toHaveURL(/subscription%3As1|subscription:s1/);
  });

  test("Preview says why it is off until something is chosen, and the red actions are quiet", async ({ page }) => {
    const gw = subscriptionsTenant([sub("s2")]);
    await gw.install(page);
    await openTenantPage(page, `/subscriptions?sheet=${encodeURIComponent("subscription:s2")}`);
    const hint = page.getByTestId("change-preview-hint");
    await expect(hint).toBeVisible();
    await expect(page.getByRole("button", { name: "Preview" })).toBeDisabled();
    await page.getByTestId("change-seats").fill("4");
    await expect(hint).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Preview" })).toBeEnabled();
    // "End now" is an outline button (not the solid red one) and still asks first.
    const end = page.getByTestId("sub-end-now");
    await expect(end).toBeVisible();
    await expect(end).not.toHaveClass(/bg-destructive/);
    await end.click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("End this subscription now?");
    await page.getByTestId("confirm-cancel").click();
    expect(gw.calls.some((c) => c.method === "POST" && c.path.startsWith("/v1/subscriptions"))).toBe(false);
  });
});

test.describe("choices kept in the address (UI-15)", () => {
  test("the tab of the usage page survives a reload", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(usageReads).on((c) => (c.method === "GET" && c.path === "/v1/meters" ? { body: { meters: METERS } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/usage");
    await page.getByTestId("usage-tab-customers").click();
    await expect(page).toHaveURL(/tab=customers/);
    await page.reload();
    await expect(page.getByTestId("usage-tab-customers")).toHaveAttribute("aria-selected", "true");
    // The first tab is the plain address.
    await page.getByTestId("usage-tab-overview").click();
    await expect(page).not.toHaveURL(/tab=/);
  });

  test("a sheet keeps the environment in the address", async ({ page }) => {
    const gw = subscriptionsTenant([sub("s2")]);
    await gw.install(page);
    await openTenantPage(page, `/subscriptions?env=sandbox&sheet=${encodeURIComponent("subscription:s2")}`);
    await expect(page.getByTestId("detail-position")).toBeVisible();
    expect(new URL(page.url()).searchParams.get("env")).toBe("sandbox");
  });
});

test.describe("position in a sheet (UI-14)", () => {
  test("the position in a sheet reads left to right", async ({ page }) => {
    const gw = subscriptionsTenant([sub("s1"), sub("s2", { plan_code: "pro" })]);
    await gw.install(page);
    await openTenantPage(page, `/subscriptions?sheet=${encodeURIComponent("subscription:s1")}`);
    const pos = page.getByTestId("detail-position");
    await expect(pos).toHaveText("1 / 2");
    await expect(pos).toHaveAttribute("dir", "ltr");
  });
});

test.describe("sign-in notice (UI-29)", () => {
  test("a typed ?expired=1 shows the same notice as the encoded one", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await page.goto("/signin?expired=1");
    await expect(page.getByRole("status").filter({ hasText: "Your session has ended" })).toBeVisible();
  });
});

test.describe("the invite page (UI-29)", () => {
  test("an unusable link says so without a leftover checking line", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await page.goto("/invite");
    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByText("Checking the invitation")).toHaveCount(0);
  });
});
