import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator, TENANT } from "./gateway";
import { APP, appWithLive, liveGateway, productGateway, signInAsAda } from "./product";
import { confirmDialog } from "./ui";

// Plan 14: what the owners of a held app are shown (the date their data is kept until, the appeal, a status scheduled ahead) and what the operator does about it (the
// schedule and the retention deadline of a tenant, the appeals and their decision).

test.use({ viewport: { width: 1440, height: 900 } });

const appeal = (over: Record<string, unknown> = {}) => ({
  id: "ap1", tenant_id: TENANT, status: "open", held_status: "banned", statement: "The cards were a test.", submitted_by: "ada@example.com", answer: "", decided_by: "",
  create_time: "2026-10-20T10:00:00Z", decide_time: null, ...over,
});

test.describe("the owner of a held app", () => {
  test("sees when the data is kept until, appeals once, and is told the appeal is waiting", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("BANNED")] });
    liveGateway(gw, { status: "BANNED" });
    let sent: unknown = null;
    let latest: unknown = null;
    gw.on((c) => {
      if (c.path === `/app/v1/apps/${APP}/hold` && c.method === "GET") return { body: { status: "TENANT_STATUS_BANNED", retention_until: "2027-01-20T10:00:00Z", appeal: latest, can_appeal: latest === null } };
      if (c.path === `/app/v1/apps/${APP}/appeal` && c.method === "POST") {
        sent = c.body;
        latest = appeal();
        return { status: 201, body: latest };
      }
      return undefined;
    });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    await expect(page.getByTestId("hold-retention")).toContainText("Your data is kept until");
    await page.getByTestId("hold-appeal-open-form").click();
    await expect(page.getByTestId("hold-appeal-submit")).toBeDisabled(); // nothing written yet
    await page.getByTestId("hold-appeal-statement").fill("The cards were a customer's test; we stopped it.");
    await page.getByTestId("hold-appeal-submit").click();
    await expect(page.getByTestId("hold-appeal-open")).toContainText("The operator has not answered yet");
    await expect(page.getByTestId("hold-appeal-open-form")).toHaveCount(0);
    expect(sent).toEqual({ statement: "The cards were a customer's test; we stopped it." });
    // The operator's category and note are never on the page.
    expect(await page.getByTestId("hold-banner").innerText()).not.toMatch(/fraud|nonpayment/i);
  });

  test("who cannot appeal reads the lines and has no form; a decided appeal shows what the operator wrote", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("SUSPENDED")] });
    liveGateway(gw, { status: "SUSPENDED" });
    gw.on((c) =>
      c.path === `/app/v1/apps/${APP}/hold` && c.method === "GET"
        ? { body: { status: "TENANT_STATUS_SUSPENDED", can_appeal: false, appeal: appeal({ status: "rejected", answer: "The dispute is not closed.", decide_time: "2026-10-22T10:00:00Z", held_status: "suspended" }) } }
        : undefined,
    );
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    await expect(page.getByTestId("hold-appeal-rejected")).toContainText("was not accepted");
    await expect(page.getByTestId("hold-appeal-answer")).toContainText("The dispute is not closed.");
    await expect(page.getByTestId("hold-appeal-open-form")).toHaveCount(0);
  });

  test("is told ahead of a scheduled suspension of an app that is still in service", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
    liveGateway(gw, { status: "ACTIVE" });
    gw.on((c) => (c.path === `/app/v1/apps/${APP}/hold` && c.method === "GET" ? { body: { status: "TENANT_STATUS_ACTIVE", scheduled_status: "suspended", scheduled_time: "2026-12-01T09:00:00Z", can_appeal: false } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    await expect(page.getByTestId("hold-scheduled")).toContainText("will be suspended on");
    await expect(page.getByTestId("hold-banner")).toHaveCount(0); // nothing is held yet
  });
});

test.describe("the operator", () => {
  function gateway(initial: { status?: string; lifecycle?: Record<string, unknown>; appeals?: Record<string, unknown>[] } = {}) {
    let status = initial.status ?? "TENANT_STATUS_ACTIVE";
    let lifecycle: Record<string, unknown> = initial.lifecycle ?? {};
    const appeals = initial.appeals ?? [];
    const gw = baseTenant(new MockGateway());
    gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}` ? { body: { id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", tier: "TENANT_TIER_STANDARD", currency: "TRY", timezone: "Europe/Istanbul", status } } : undefined));
    gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/status-events` ? { body: { events: [] } } : undefined));
    gw.on((c) => (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/lifecycle` ? { body: lifecycle } : undefined));
    gw.on((c) => {
      if (c.path !== `/admin/v1/tenants/${TENANT}/scheduled-status`) return undefined;
      if (c.method === "PUT") {
        lifecycle = { scheduled_status: c.body?.status, scheduled_time: c.body?.schedule_time, scheduled_category: c.body?.category ?? "", scheduled_reason: c.body?.reason ?? "", scheduled_by: "operator@example.com" };
      } else lifecycle = {};
      return { body: lifecycle };
    });
    gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/appeals" ? { body: { appeals: appeals.filter((a) => !c.query.get("status") || a.status === c.query.get("status")), app_names: { [TENANT]: "Acme" } } } : undefined));
    gw.on((c) => {
      if (c.method !== "POST" || !c.path.startsWith("/admin/v1/appeals/")) return undefined;
      const a = appeals[0]!;
      Object.assign(a, { status: c.body?.decision, answer: c.body?.answer ?? "", decided_by: "operator@example.com", decide_time: "2026-10-22T10:00:00Z" });
      if (c.body?.decision === "accepted") status = "TENANT_STATUS_ACTIVE";
      return { body: a };
    });
    return gw;
  }

  test("schedules a suspension for an active live tenant, sees it, and cancels it after a question", async ({ page }) => {
    const gw = gateway();
    await gw.install(page);
    await openTenantPage(page, "");
    const card = page.getByTestId("tenant-lifecycle");
    await expect(card.getByTestId("lifecycle-schedule")).toContainText("Nothing is scheduled.");
    await expect(card.getByTestId("schedule-submit")).toBeDisabled(); // no time yet
    await card.getByTestId("schedule-status").selectOption("frozen");
    await card.getByTestId("schedule-time").fill("2030-01-15T09:30");
    await card.getByTestId("schedule-category").selectOption("nonpayment");
    await card.getByTestId("schedule-reason").fill("platform invoice unpaid");
    await card.getByTestId("schedule-submit").click();
    await expect(card.getByTestId("lifecycle-schedule")).toContainText("Frozen on");
    const sent = gw.called("PUT", `/admin/v1/tenants/${TENANT}/scheduled-status`)[0]?.body as Record<string, string>;
    expect(sent).toMatchObject({ status: "frozen", category: "nonpayment", reason: "platform invoice unpaid" });
    expect(new Date(sent.schedule_time!).getTime()).toBe(new Date("2030-01-15T09:30").getTime());
    await card.getByTestId("lifecycle-cancel").click();
    await confirmDialog(page, "no");
    expect(gw.called("DELETE", `/admin/v1/tenants/${TENANT}/scheduled-status`)).toHaveLength(0);
    await card.getByTestId("lifecycle-cancel").click();
    await confirmDialog(page, "yes");
    await expect(card.getByTestId("lifecycle-schedule")).toContainText("Nothing is scheduled.");
  });

  test("sees the retention deadline of a banned tenant, and that the job has marked it due and deleted nothing", async ({ page }) => {
    const gw = gateway({ status: "TENANT_STATUS_BANNED", lifecycle: { retention_until: "2026-10-01T10:00:00Z", retention_due_time: "2026-10-01T10:01:00Z" } });
    await gw.install(page);
    await openTenantPage(page, "");
    const card = page.getByTestId("tenant-lifecycle");
    await expect(card.getByTestId("lifecycle-retention-due")).toContainText("Nothing is deleted automatically");
    await expect(card.getByTestId("schedule-submit")).toHaveCount(0); // a banned tenant has nothing to schedule
  });

  test("reads an appeal, needs an answer to reject it, and asks before accepting", async ({ page }) => {
    const gw = gateway({ status: "TENANT_STATUS_BANNED", appeals: [appeal()] });
    await gw.install(page);
    await openTenantPage(page, "");
    const card = page.getByTestId("tenant-appeals");
    await expect(card).toContainText("The cards were a test.");
    await card.getByTestId("appeal-decide-ap1").click();
    await expect(page.getByTestId("appeal-reject")).toBeDisabled(); // an owner whose appeal is refused is owed a sentence
    await page.getByTestId("appeal-answer").fill("The hold stays.");
    await page.getByTestId("appeal-reject").click();
    await expect(card.getByTestId("appeal-ap1")).toContainText("Rejected");
    expect(gw.called("POST", "/admin/v1/appeals/ap1/decision")[0]?.body).toEqual({ decision: "rejected", answer: "The hold stays." });
  });

  test("accepting asks first", async ({ page }) => {
    const gw = gateway({ status: "TENANT_STATUS_BANNED", appeals: [appeal()] });
    await gw.install(page);
    await openTenantPage(page, "");
    await page.getByTestId("appeal-decide-ap1").click();
    await page.getByTestId("appeal-answer").fill("Welcome back.");
    await page.getByTestId("appeal-accept").click();
    await confirmDialog(page, "no");
    expect(gw.called("POST", "/admin/v1/appeals/ap1/decision")).toHaveLength(0);
    await page.getByTestId("appeal-accept").click();
    await confirmDialog(page, "yes");
    await expect(page.getByTestId("appeal-ap1")).toContainText("Accepted");
    expect(gw.called("POST", "/admin/v1/appeals/ap1/decision")[0]?.body).toEqual({ decision: "accepted", answer: "Welcome back." });
  });

  test("finds the open appeals of every tenant on the list of tenants", async ({ page }) => {
    const gw = gateway({ appeals: [appeal()] });
    gw.on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
    await gw.install(page);
    await signInAsOperator(page);
    const card = page.getByTestId("tenant-appeals");
    await expect(card).toContainText("Open appeals");
    await expect(card.getByTestId("appeal-tenant-ap1")).toHaveAttribute("href", new RegExp(`/tenants/${TENANT}$`));
  });
});
