import { expect, test, type Page } from "@playwright/test";
import { MockGateway, OPERATOR, TENANT, auditEvent, auditGateway, signInAsOperator, type MockAuditEvent } from "./gateway";
import { LIVE, SESSION, productGateway, signInAsAda } from "./product";
import { pickLanguage } from "./ui";

// The audit log (plan 22, G4): a tenant's page for a person, the operator's page across tenants, the filters (they wait for their Apply), paging, the
// sheet with every field of an entry and the CSV export of the same filter. The gateway is mocked; it filters and pages the way the real one does.

const OTHER_TENANT = "0192f0b4-0000-7c3a-9a3e-6a1f2b3c4d5e";
const rows = (page: Page) => page.locator('[data-testid^="audit-row-"]');
const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

/** Twelve entries, newest first: keys, members, a refund, a refused sign-in of the system, an API key acting. */
function log(): MockAuditEvent[] {
  return [
    auditEvent(1, { action: "api_key.created", summary: "created the key abcd1" }),
    auditEvent(2, { action: "member.role_changed", target_type: "user", target_id: "u2", summary: "made Grace Hopper an administrator", actor_email: "ada@example.com", account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", tenant_id: "" }),
    auditEvent(3, { action: "credit_note.refunded", actor_type: "system", actor_id: "billing-svc", actor_email: "", target_type: "credit_note", target_id: "cn1", summary: "refunded 49.00 USD" }),
    auditEvent(4, { action: "api_key.revoked", actor_type: "api_key", actor_id: "key_42", actor_email: "", summary: "revoked the key abcd0" }),
    auditEvent(5, { action: "something.brand_new", summary: "an action the console has no words for yet" }),
    auditEvent(6, { action: "webhook_endpoint.created", actor_email: "grace@example.com", actor_id: "22222222-2222-4222-8222-222222222222", summary: "created the webhook endpoint https://example.com/hook" }),
    ...Array.from({ length: 6 }, (_, i) => auditEvent(7 + i, { action: "api_key.created", summary: `created the key old${i}` })),
  ];
}

async function openLog(page: Page, gw: MockGateway) {
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/audit`);
  await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
}

test.describe("the audit log of a tenant", () => {
  test("lists the entries newest first with a translated action and the raw action where the console has no words for it", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(12);
    const first = rows(page).first();
    await expect(first).toContainText("API key created");
    await expect(first).toContainText("ada@example.com");
    await expect(first).toContainText("Person");
    await expect(first).toContainText("created the key abcd1");
    await expect(first).toContainText("api_key · k1");
    // An actor without an address is named by its id, and the type says what it is.
    await expect(rows(page).nth(2)).toContainText("System");
    await expect(rows(page).nth(2)).toContainText("billing-svc");
    await expect(rows(page).nth(3)).toContainText("API key");
    await expect(rows(page).nth(3)).toContainText("key_42");
    await expect(rows(page).nth(4)).toContainText("something.brand_new");
    // The tenant API is spoken as the person: their session plus the tenant, with the default page size.
    const call = gw.called("GET", "/v1/audit-events")[0];
    expect(call?.headers["authorization"]).toBe(`Bearer ${SESSION}`);
    expect(call?.headers["x-payrudder-tenant"]).toBe(LIVE);
    expect(call?.query.get("page_size")).toBe("50");
    expect(call?.query.get("action")).toBeNull();
    // No bulk bar and no sort: a log is read, in the server's order.
    await expect(page.getByRole("checkbox")).toHaveCount(0);
    await expect(page.getByTestId("sort-select")).toHaveCount(0);
  });

  test("the area filter waits for Apply, then asks for <area>.* and narrows the rows", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(12);
    const before = gw.called("GET", "/v1/audit-events").length;

    await page.getByRole("button", { name: "Area", exact: true }).click();
    const list = page.getByRole("listbox", { name: "Area" });
    await list.getByRole("option", { name: "API keys" }).click();
    // A choice in the list is a draft: nothing has been asked and nothing changed.
    expect(gw.called("GET", "/v1/audit-events")).toHaveLength(before);
    await expect(rows(page)).toHaveCount(12);
    await page.getByTestId("filter-apply").click();

    await expect.poll(() => gw.called("GET", "/v1/audit-events").length).toBe(before + 1);
    expect(gw.called("GET", "/v1/audit-events").at(-1)?.query.get("action")).toBe("api_key.*");
    await expect(rows(page)).toHaveCount(8);
    await expect(page.getByText("created the webhook endpoint")).toHaveCount(0);

    // Two areas are two values; the cross on the chip takes the filter off at once.
    await page.getByRole("button", { name: "Area", exact: false }).first().click();
    await page.getByRole("listbox", { name: "Area" }).getByRole("option", { name: "Members" }).click();
    await page.getByTestId("filter-apply").click();
    await expect.poll(() => gw.called("GET", "/v1/audit-events").at(-1)?.query.get("action")).toBe("api_key.*,member.*");
    await expect(rows(page)).toHaveCount(9);
    await page.getByRole("button", { name: "Area: ×" }).click();
    await expect(rows(page)).toHaveCount(12);
    await expect(page.getByRole("button", { name: "Area: ×" })).toHaveCount(0);
  });

  test("the actor is a search that applies when its box is left or Enter is pressed, by address or by id", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(12);
    const before = gw.called("GET", "/v1/audit-events").length;
    const box = page.getByRole("searchbox", { name: "Actor: e-mail or id" });
    await box.fill("grace@example.com");
    // Typing alone asks for nothing.
    expect(gw.called("GET", "/v1/audit-events")).toHaveLength(before);
    await box.press("Enter");
    await expect(rows(page)).toHaveCount(1);
    expect(gw.called("GET", "/v1/audit-events").at(-1)?.query.get("actor")).toBe("grace@example.com");
    await expect(rows(page).first()).toContainText("created the webhook endpoint");

    // Leaving the box applies too: by the actor's id this time.
    await box.fill("key_42");
    await page.getByRole("heading", { name: "Audit log" }).click();
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("revoked the key abcd0");
    expect(gw.called("GET", "/v1/audit-events").at(-1)?.query.get("actor")).toBe("key_42");

    // "Clear filters" empties the box as well.
    await page.getByRole("button", { name: "Clear filters" }).click();
    await expect(rows(page)).toHaveCount(12);
    await expect(box).toHaveValue("");
  });

  test("the date range applies on its own button: from is the start of the first day, to the start of the day after the last (exclusive)", async ({ page }) => {
    // The mock's clock is 21 Oct 2026 12:00: the entries are an hour apart from 10:00 UTC that day.
    const events = [auditEvent(1), auditEvent(2, { create_time: "2026-10-20T10:00:00Z", summary: "yesterday" }), auditEvent(3, { create_time: "2026-10-10T10:00:00Z", summary: "ten days ago" })];
    const gw = auditGateway(productGateway(new MockGateway()).gw, events);
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(3);
    await page.getByTestId("date-range").click();
    await page.getByRole("button", { name: "Last 7 days" }).click();
    await expect(rows(page)).toHaveCount(2);
    const q = gw.called("GET", "/v1/audit-events").at(-1)?.query;
    expect(q?.get("from")).toBe(new Date(2026, 9, 15).toISOString());
    expect(q?.get("to")).toBe(new Date(2026, 9, 22).toISOString());
    await expect(page.getByText("ten days ago")).toHaveCount(0);
  });

  test("Load more appends the next page and goes away on the last one", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log(), { perPage: 5 });
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(5);
    await page.getByTestId("audit-more").click();
    await expect(rows(page)).toHaveCount(10);
    // The first page is still there, above the new one, and the token of the first was used.
    await expect(rows(page).first()).toContainText("created the key abcd1");
    expect(gw.called("GET", "/v1/audit-events")[1]?.query.get("page_token")).toBe("o5");
    await page.getByTestId("audit-more").click();
    await expect(rows(page)).toHaveCount(12);
    await expect(page.getByTestId("audit-more")).toHaveCount(0);
  });

  test("a service that did not answer is named in a warning, and the entries that did come are shown", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log().slice(0, 3), { incomplete: ["billing", "payments"] });
    await openLog(page, gw);
    await expect(rows(page)).toHaveCount(3);
    const warning = page.getByTestId("audit-incomplete");
    await expect(warning).toContainText("Some entries may be missing");
    await expect(warning).toContainText("invoices and credit notes");
    await expect(warning).toContainText("payment provider accounts");
    await expect(warning).not.toContainText("plans");
  });

  test("a row opens a sheet with every field of the entry, the detail pretty-printed, and the address names it", async ({ page }) => {
    const events = log();
    events[0] = auditEvent(1, { detail_json: '{"role":"read_only","environment":"live"}', before_hash: "aaaa1111", after_hash: "bbbb2222", source_ip: "203.0.113.7", request_id: "req-abc" });
    const gw = auditGateway(productGateway(new MockGateway()).gw, events);
    await openLog(page, gw);
    await rows(page).first().click();
    const sheet = page.getByTestId("audit-detail");
    await expect(sheet).toBeVisible();
    await expect(page.getByTestId("detail-sheet")).toContainText("API key created");
    for (const text of ["created the key abcd1", "ada@example.com", "11111111-1111-4111-8111-111111111111", "api_key", "k1", "203.0.113.7", "req-abc", "aaaa1111", "bbbb2222"]) await expect(sheet).toContainText(text);
    // The detail is parsed and indented, as text (never as markup).
    await expect(page.getByTestId("audit-detail-json")).toHaveText('{\n  "role": "read_only",\n  "environment": "live"\n}');
    expect(page.url()).toContain(`sheet=audit%3A${events[0]?.id}`);
    // The arrows step through the entries that are loaded.
    await page.getByTestId("detail-next").click();
    await expect(sheet).toContainText("made Grace Hopper an administrator");
    await page.getByTestId("detail-prev").click();
    await expect(sheet).toContainText("created the key abcd1");
  });

  test("a reload with the sheet in the address opens it again, even on a page that was not loaded yet", async ({ page }) => {
    const events = log();
    const gw = auditGateway(productGateway(new MockGateway()).gw, events, { perPage: 5 });
    await openLog(page, gw);
    await page.goto(`/app/t/${LIVE}/audit?sheet=audit%3A${events[7]?.id}`);
    await expect(page.getByTestId("audit-detail")).toContainText("created the key old1");
    expect(gw.called("GET", "/v1/audit-events").some((c) => c.query.get("page_token") === "o5")).toBe(true);
  });

  test("the CSV is the server's export of the same filter, fetched with the session and saved as audit-log.csv", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await openLog(page, gw);
    await page.getByRole("button", { name: "Area", exact: true }).click();
    await page.getByRole("listbox", { name: "Area" }).getByRole("option", { name: "API keys" }).click();
    await page.getByTestId("filter-apply").click();
    await expect(rows(page)).toHaveCount(8);
    const box = page.getByRole("searchbox", { name: "Actor: e-mail or id" });
    await box.fill("ada@example.com");
    await box.press("Enter");
    await expect(rows(page)).toHaveCount(7);

    const download = page.waitForEvent("download");
    await page.getByTestId("audit-export").click();
    const file = await download;
    expect(file.suggestedFilename()).toBe("audit-log.csv");
    const call = gw.called("GET", "/v1/audit-events/export")[0];
    expect(call?.query.get("action")).toBe("api_key.*");
    expect(call?.query.get("actor")).toBe("ada@example.com");
    expect(call?.headers["authorization"]).toBe(`Bearer ${SESSION}`);
    expect(call?.headers["x-payrudder-tenant"]).toBe(LIVE);
  });

  test("an export the server cannot make whole says so and saves nothing", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log(), { exportFails: true });
    await openLog(page, gw);
    await page.getByTestId("audit-export").click();
    await expect(page.getByRole("alert")).toContainText("could not be read from every service");
  });

  test("a viewer does not see the page in the sidebar, and the API's refusal is shown as the standard error when the address is opened", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", account_name: "Ada Labs", role: "viewer" }] });
    auditGateway(gw, log(), { forbidden: true });
    await gw.install(page);
    await signInAsAda(page);
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "E-mail" })).toBeVisible();
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "Audit log" })).toHaveCount(0);
    await page.goto(`/app/t/${LIVE}/audit`);
    await expect(page.getByTestId("error-state")).toContainText("You do not have access");
    await expect(rows(page)).toHaveCount(0);
  });

  test("an owner finds it in the sidebar, in the app's settings after Mail", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await openLog(page, gw);
    const link = page.getByTestId("sidebar").getByRole("link", { name: "Audit log" });
    await expect(link).toHaveAttribute("href", new RegExp(`/app/t/${LIVE}/audit`));
    await expect(link).toHaveAttribute("aria-current", "page");
  });
});

test.describe("the operator's audit log", () => {
  async function openOperatorLog(page: Page, events: MockAuditEvent[]) {
    const gw = new MockGateway().on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
    auditGateway(gw, events);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto("/audit");
    await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
    return gw;
  }

  test("reads every tenant with the operator's session, and the tenant id is a filter that applies when its box is left", async ({ page }) => {
    const events = [auditEvent(1), auditEvent(2, { tenant_id: OTHER_TENANT, summary: "changed the status of another tenant", action: "tenant.status_changed", target_type: "tenant" }), auditEvent(3, { tenant_id: "", account_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", action: "member.removed" })];
    const gw = await openOperatorLog(page, events);
    await expect(rows(page)).toHaveCount(3);
    const first = gw.called("GET", "/admin/v1/audit-events")[0];
    expect(first?.headers["authorization"]).toBe(`Bearer ${OPERATOR}`);
    expect(first?.headers["x-payrudder-tenant"]).toBeUndefined();
    expect(first?.query.get("tenant_id")).toBeNull();
    // Its platform door is in the sidebar, current on this page.
    await expect(page.getByTestId("sidebar").getByRole("link", { name: "Audit log" })).toHaveAttribute("aria-current", "page");

    const tenant = page.getByTestId("audit-tenant");
    await tenant.fill(OTHER_TENANT);
    expect(gw.called("GET", "/admin/v1/audit-events")).toHaveLength(1);
    await tenant.press("Enter");
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText("changed the status of another tenant");
    expect(gw.called("GET", "/admin/v1/audit-events").at(-1)?.query.get("tenant_id")).toBe(OTHER_TENANT);

    // The sheet of an operator's entry also names the tenant and the account.
    await rows(page).first().click();
    await expect(page.getByTestId("audit-detail")).toContainText(OTHER_TENANT);
  });

  test("a tenant id that is not one is the server's refusal, in words", async ({ page }) => {
    const gw = await openOperatorLog(page, [auditEvent(1)]);
    gw.on((c) =>
      c.path === "/admin/v1/audit-events" && c.query.get("tenant_id") === "nope"
        ? { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "audit.filter.bad_tenant", message: "bad tenant" } } }
        : undefined,
    );
    const tenant = page.getByTestId("audit-tenant");
    await tenant.fill("nope");
    await tenant.press("Enter");
    await expect(page.getByTestId("error-state")).toContainText("The tenant id is not valid.");
  });

  test("the export of the operator's log carries the tenant filter", async ({ page }) => {
    const gw = await openOperatorLog(page, [auditEvent(1), auditEvent(2, { tenant_id: OTHER_TENANT })]);
    const tenant = page.getByTestId("audit-tenant");
    await tenant.fill(OTHER_TENANT);
    await tenant.press("Enter");
    await expect(rows(page)).toHaveCount(1);
    const download = page.waitForEvent("download");
    await page.getByTestId("audit-export").click();
    expect((await download).suggestedFilename()).toBe("audit-log.csv");
    expect(gw.called("GET", "/admin/v1/audit-events/export")[0]?.query.get("tenant_id")).toBe(OTHER_TENANT);
  });

  test("a person who is not an operator is sent home from the address", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/audit");
    await page.waitForURL(/\/app(\/apps\/[^/?]+)?(?![^?])/);
    await expect(page.getByRole("heading", { name: "Audit log" })).toHaveCount(0);
  });

  test("the operator also reads one tenant's log from the tenant's own pages", async ({ page }) => {
    const gw = new MockGateway().on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
    auditGateway(gw, [auditEvent(1)]);
    await gw.install(page);
    await signInAsOperator(page);
    await page.goto(`/tenants/${TENANT}/audit`);
    await expect(rows(page)).toHaveCount(1);
    const call = gw.called("GET", "/v1/audit-events")[0];
    expect(call?.headers["x-payrudder-tenant"]).toBe(TENANT);
    expect(call?.headers["authorization"]).toBe(`Bearer ${OPERATOR}`);
  });
});

test.describe("in Arabic and on a phone", () => {
  test("the log reads right to left and nothing is wider than the screen", async ({ page }) => {
    const gw = auditGateway(productGateway(new MockGateway()).gw, log());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/audit`);
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await expect(page.getByRole("heading", { name: "سجل التدقيق" })).toBeVisible();
    await expect(rows(page)).toHaveCount(12);
    await expect(rows(page).first()).toContainText("تم إنشاء مفتاح API");
    await page.setViewportSize({ width: 360, height: 740 });
    await expect(rows(page).first()).toBeVisible();
    await expect.poll(() => sideways(page)).toBeLessThanOrEqual(0);
    await rows(page).first().click();
    await expect(page.getByTestId("audit-detail")).toBeVisible();
    await expect.poll(() => sideways(page)).toBeLessThanOrEqual(0);
  });
});
