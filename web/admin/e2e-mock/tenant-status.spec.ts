import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { confirmDialog, pickFilter, pickFilters } from "./ui";

// The operator's hand on a tenant: freeze with a category and a reason, read the history, lift it; a ban needs a reason, and where it needs a second operator the first
// request says it is waiting.

test.use({ viewport: { width: 1440, height: 900 } });

function gateway(opts: { approval?: boolean; historyDown?: boolean } = {}) {
  let status = "TENANT_STATUS_ACTIVE";
  const events: Record<string, unknown>[] = [];
  const names: Record<string, string> = { active: "TENANT_STATUS_ACTIVE", suspended: "TENANT_STATUS_SUSPENDED", frozen: "TENANT_STATUS_FROZEN", banned: "TENANT_STATUS_BANNED" };
  const gw = baseTenant(new MockGateway());
  gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}` ? { body: { id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", tier: "TENANT_TIER_STANDARD", currency: "TRY", timezone: "Europe/Istanbul", status } } : undefined));
  gw.on((c) => {
    if (c.method !== "GET" || c.path !== `/admin/v1/tenants/${TENANT}/status-events`) return undefined;
    return opts.historyDown ? { status: 503, body: { error: { code: "UNAVAILABLE", key: "", message: "status history is down" } } } : { body: { events } };
  });
  gw.on((c) => {
    if (c.method !== "PUT" || c.path !== `/admin/v1/tenants/${TENANT}/status`) return undefined;
    const to = names[String(c.body?.status)]!;
    const row = { id: `e${events.length}`, from_status: status, to_status: to, category: c.body?.category ?? "", reason: c.body?.reason ?? "", actor: "operator@example.com", approved_by: "", create_time: "2026-10-21T10:00:00Z" };
    if (opts.approval && to === "TENANT_STATUS_BANNED") {
      events.unshift({ ...row, kind: "requested" });
      return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "plans.tenant.approval_pending", message: "waiting" } } };
    }
    events.unshift({ ...row, kind: "changed" });
    status = to;
    return { body: { id: TENANT, status } };
  });
  return gw;
}

test("a failed read of the status history says so and does not claim that nothing changed yet", async ({ page }) => {
  const gw = gateway({ historyDown: true });
  await gw.install(page);
  await openTenantPage(page, "");
  const history = page.getByTestId("tenant-status").getByTestId("tenant-status-history");
  await expect(history).toContainText("status history is down");
  await expect(history).not.toContainText("No change yet.");
});

test("freezing a tenant asks, keeps the category and the reason, and shows the history; lifting it is one click", async ({ page }) => {
  const gw = gateway();
  await gw.install(page);
  await openTenantPage(page, "");
  const card = page.getByTestId("tenant-status");
  await expect(card.getByTestId("tenant-status-current")).toHaveText("Active");
  await expect(card.getByTestId("tenant-status-history")).toContainText("No change yet.");

  await card.getByTestId("status-to").selectOption("frozen");
  await expect(card.getByTestId("status-effect")).toContainText("Read-only");
  await card.getByTestId("status-category").selectOption("nonpayment");
  await card.getByTestId("status-reason").fill("unpaid platform invoice");
  await card.getByTestId("status-apply").click();
  await confirmDialog(page);
  await expect(card.getByTestId("tenant-status-current")).toHaveText("Frozen");
  expect(gw.called("PUT", `/admin/v1/tenants/${TENANT}/status`)[0]?.body).toMatchObject({ status: "frozen", category: "nonpayment", reason: "unpaid platform invoice" });
  const line = card.getByTestId("tenant-status-event");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("Active → Frozen");
  await expect(line).toContainText("Non-payment");
  await expect(line).toContainText("unpaid platform invoice");
  await expect(line).toContainText("operator@example.com");

  await card.getByTestId("status-to").selectOption("active");
  await card.getByTestId("status-apply").click();
  await confirmDialog(page);
  await expect(card.getByTestId("tenant-status-current")).toHaveText("Active");
  await expect(line).toHaveCount(2);
});

test("a ban needs a reason, and where it needs a second operator the first request is recorded as waiting", async ({ page }) => {
  const gw = gateway({ approval: true });
  await gw.install(page);
  await openTenantPage(page, "");
  const card = page.getByTestId("tenant-status");
  await card.getByTestId("status-to").selectOption("banned");
  await expect(card.getByTestId("status-apply")).toBeDisabled();
  await card.getByTestId("status-reason").fill("card testing");
  await expect(card.getByTestId("status-apply")).toBeEnabled();
  await card.getByTestId("status-apply").click();
  await confirmDialog(page);
  await expect(card).toContainText("A second operator must make the same request");
  await expect(card.getByTestId("tenant-status-current")).toHaveText("Active");
  await expect(card.getByTestId("tenant-status-event")).toContainText("waiting for a second operator");
});

test("the history is filtered by where the tenant went, the category and the kind, each on its button", async ({ page }) => {
  const gw = gateway();
  const row = (id: string, kind: string, from: string, to: string, category: string, reason: string) => ({ id, kind, from_status: from, to_status: to, category, reason, actor: "operator@example.com", approved_by: "", create_time: "2026-10-21T10:00:00Z" });
  const A = "TENANT_STATUS_ACTIVE", S = "TENANT_STATUS_SUSPENDED", F = "TENANT_STATUS_FROZEN", B = "TENANT_STATUS_BANNED";
  gw.on((c) =>
    c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/status-events`
      ? { body: { events: [row("e4", "changed", B, A, "other", "appeal accepted"), row("e3", "requested", F, B, "fraud", "card testing"), row("e2", "changed", S, F, "nonpayment", "unpaid invoice"), row("e1", "changed", A, S, "abuse", "spam")] } }
      : undefined,
  );
  await gw.install(page);
  await openTenantPage(page, "");
  const history = page.getByTestId("tenant-status-history");
  const lines = history.getByTestId("tenant-status-event");
  await expect(lines).toHaveCount(4);
  await expect(history.getByTestId("tenant-status-count")).toHaveCount(0);

  await pickFilter(page, "Changed to", "Suspended");
  await expect(lines).toHaveCount(1);
  await expect(lines).toContainText("spam");
  await expect(history.getByTestId("tenant-status-count")).toHaveText("1 of 4 changes");

  await pickFilter(page, "Kind", "Waiting for a second operator");
  await expect(history.getByTestId("tenant-status-nomatch")).toBeVisible();
  await expect(lines).toHaveCount(0);
  // The cross of a chip clears that filter at once: the waiting request alone is left once the status is cleared.
  await page.getByRole("button", { name: "Changed to: ×" }).click();
  await expect(lines).toHaveCount(1);
  await expect(lines).toContainText("card testing");
  await page.getByRole("button", { name: "Kind: ×" }).click();
  await pickFilters(page, "Category", "Non-payment", "Abuse");
  await expect(lines).toHaveCount(2);
  await expect(history.getByTestId("tenant-status-count")).toHaveText("2 of 4 changes");
});
