import { expect, test } from "@playwright/test";
import { pickPlace } from "./ui";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

// Free credits and trials for the tenant's customers (phase 52): credits granted from a customer's sheet with a reason and an expiry, trial subscriptions
// (days, no card) with a default on the package, trial ends shown and reminded, and a free-tier package.

const DAY = 86_400_000;
// The mocks run on 21 October 2026 (gateway.ts), whatever the day the suite runs on.
const NOW = new Date(2026, 9, 21, 12, 0, 0).getTime();
const inDays = (n: number) => new Date(NOW + n * DAY).toISOString();
const customers = [
  { id: "id-1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", create_time: "2026-10-01T10:00:00Z" },
  { id: "id-2", external_id: "cus_2", name: "Grace Hopper", email: "grace@example.com", create_time: "2026-10-02T10:00:00Z" },
];
const sub = (id: string, customer: string, extra: Record<string, unknown> = {}) => ({
  id, customer_external_id: customer, plan_code: "pro", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", interval: "SUBSCRIPTION_INTERVAL_MONTH", currency: "TRY", start_time: "2026-10-01T10:00:00Z", fee_timing: "FEE_TIMING_ADVANCE", ...extra,
});

test.describe("free credits", () => {
  function creditsGateway() {
    let balance = 1200;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/customers") return { body: { customers } };
      if (c.path === "/v1/invoices") return { body: { invoices: [] } };
      if (c.path === "/v1/payments") return { body: { payments: [] } };
      if (c.path === "/v1/subscriptions") return { body: { subscriptions: [] } };
      if (c.path === "/v1/customers/cus_1/credits/grant") {
        balance += Number(c.body?.credits);
        return { body: { balance: String(balance), replayed: false } };
      }
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [], next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits") return { body: { balance: String(balance), lots: [] } };
      if (/^\/v1\/customers\/cus_\d\/credits(\/ledger)?$/.test(c.path)) return { body: { balance: "0", lots: [], entries: [] } };
      return undefined;
    });
    return gw;
  }

  test("a customer's sheet grants free credits with a reason and an expiry, and the balance follows", async ({ page }) => {
    const gw = creditsGateway();
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customer-cus_1").click();
    const sheet = page.getByTestId("customer-detail");
    await expect(sheet.getByTestId("customer-credits")).toHaveText("1,200");
    await sheet.getByTestId("customer-tab-credits").click();
    await sheet.getByTestId("customer-grant-credits").click();

    const form = page.getByRole("form", { name: "Add credits" });
    await expect(form).toBeVisible();
    // A welcome gift that never expires is what it starts as.
    await expect(form.getByLabel("Reason type")).toHaveValue("welcome");
    await expect(form.getByLabel("Expires")).toHaveValue("never");
    // Nothing to grant yet: submitting says what is missing at the field and sends nothing.
    await page.getByTestId("grant-submit").click();
    await expect(form.locator("#grant-credits-error")).toContainText("whole number");
    expect(gw.called("POST", "/v1/customers/cus_1/credits/grant")).toHaveLength(0);

    await form.getByLabel("Credits to add").fill("300");
    await form.getByLabel("Reason type").selectOption("goodwill");
    await form.getByLabel("Note (optional)").fill("late invoice");
    await form.getByLabel("Expires").selectOption("30");
    await page.getByTestId("grant-submit").click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/credits/grant").length).toBe(1);

    const body = gw.called("POST", "/v1/customers/cus_1/credits/grant")[0]?.body as Record<string, string>;
    expect(body).toMatchObject({ credits: "300", source: "manual", reason: "Goodwill: late invoice" });
    expect(body.idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
    const expires = Date.parse(body.expire_time ?? "");
    expect(expires).toBeGreaterThan(NOW + 29 * DAY);
    expect(expires).toBeLessThan(NOW + 32 * DAY);
    expect(gw.called("POST", "/v1/customers/cus_1/credits/grant")[0]?.headers["x-payrudder-tenant"]).toBeTruthy();

    // The sheet closes and the customer's balance is read again.
    await expect(form).toHaveCount(0);
    await expect(sheet.getByTestId("customer-credits")).toHaveText("1,500");
  });

  test("an expiry on a chosen date must lie in the future, and a welcome gift or promotion is a promotional lot", async ({ page }) => {
    const gw = creditsGateway();
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-detail").getByTestId("customer-tab-credits").click();
    await page.getByTestId("customer-grant-credits").click();
    const form = page.getByRole("form", { name: "Add credits" });
    await form.getByLabel("Credits to add").fill("50");
    await form.getByLabel("Expires").selectOption("date");
    await form.getByLabel("Expiry date").fill("2020-01-01");
    await page.getByTestId("grant-submit").click();
    await expect(form.getByRole("alert")).toContainText("Choose an expiry date in the future.");
    expect(gw.called("POST", "/v1/customers/cus_1/credits/grant")).toHaveLength(0);

    await form.getByLabel("Expiry date").fill("2099-12-31");
    await form.getByLabel("Reason type").selectOption("promotion");
    await page.getByTestId("grant-submit").click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/credits/grant").length).toBe(1);
    const body = gw.called("POST", "/v1/customers/cus_1/credits/grant")[0]?.body as Record<string, string>;
    expect(body).toMatchObject({ credits: "50", source: "promo", reason: "Promotion" });
    expect(new Date(body.expire_time ?? "").getFullYear()).toBe(2099);
  });

  test("the credits page grants the same way", async ({ page }) => {
    const gw = creditsGateway().on((c) => (c.path === "/v1/credit-packs" ? { body: { packs: [] } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await page.getByTestId("credits-grant").click();
    const form = page.getByRole("form", { name: "Add credits" });
    await expect(page.getByRole("dialog").getByText("Free credits go into the balance of cus_1")).toBeVisible();
    await form.getByLabel("Credits to add").fill("25");
    await page.getByTestId("grant-submit").click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/credits/grant").length).toBe(1);
    expect(gw.called("POST", "/v1/customers/cus_1/credits/grant")[0]?.body).toMatchObject({ credits: "25", source: "promo", reason: "Welcome gift" });
  });
});

test.describe("trials", () => {
  const plans = [
    { id: "p1", code: "basic", name: "Basic", latest_version: 1 },
    { id: "p2", code: "pro", name: "Pro", latest_version: 1 },
  ];
  const versions: Record<string, Record<string, unknown>> = { basic: { version: 1, base_fee_minor: "1000" }, pro: { version: 1, base_fee_minor: "9900", trial_days: 14 } };

  function trialGateway(subs: Record<string, unknown>[]) {
    return baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: subs } };
      if (c.method === "POST" && c.path === "/v1/subscriptions") return { status: 201, body: sub("s9", String(c.body?.customer_external_id)) };
      if (c.path === "/v1/customers") return { body: { customers } };
      if (c.path === "/v1/plans") return { body: { plans } };
      const v = /^\/v1\/plans\/([^/]+)\/versions\/latest$/.exec(c.path);
      if (v) return { body: versions[v[1] ?? ""] };
      if (c.path.endsWith("/entitlements")) return { body: { items: [] } };
      return undefined;
    });
  }

  test("a package's default trial is offered, left to the package when the field is empty, and overridden by a number (0 included)", async ({ page }) => {
    const gw = trialGateway([]);
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    const create = async (fill?: string) => {
      await page.getByTestId("subscriptions-new").click();
      const form = page.getByRole("form", { name: "new subscription" });
      await pickPlace(form.getByLabel("Customer"), "cus_1");
      await form.getByLabel("Package").selectOption("pro");
      await expect(form.getByLabel("Trial days")).toHaveAttribute("placeholder", "Package default: 14 days");
      await expect(form.getByTestId("trial-hint")).toContainText("No card is needed during a trial");
      if (fill !== undefined) await form.getByLabel("Trial days").fill(fill);
      await page.getByRole("dialog").getByRole("button", { name: "Create subscription" }).click();
      await expect(form).toHaveCount(0);
    };
    await create();
    await create("0");
    await create("30");
    const bodies = gw.called("POST", "/v1/subscriptions").map((c) => c.body);
    expect(bodies[0]).not.toHaveProperty("trial_days");
    expect(bodies[1]).toMatchObject({ trial_days: 0 });
    expect(bodies[2]).toMatchObject({ trial_days: 30 });
    // A package without a default trial offers none.
    await page.getByTestId("subscriptions-new").click();
    const form = page.getByRole("form", { name: "new subscription" });
    await form.getByLabel("Package").selectOption("basic");
    await expect(form.getByLabel("Trial days")).toHaveAttribute("placeholder", "Trial days");
  });

  test("a trial's end is shown in the list and the sheet, and the trials ending within a week are reminded", async ({ page }) => {
    const gw = trialGateway([
      sub("s1", "cus_1", { state: "SUBSCRIPTION_STATE_TRIALING", trial_end_time: inDays(3) }),
      sub("s2", "cus_2", { state: "SUBSCRIPTION_STATE_TRIALING", trial_end_time: inDays(40) }),
      sub("s3", "cus_3", {}),
    ]);
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");

    // The reminder counts the trials that end soon, not the ones that last longer.
    await expect(page.getByTestId("trials-ending")).toContainText("1 trial ends within 7 days");
    const rows = page.getByRole("row");
    await expect(rows.filter({ hasText: "cus_1" })).toContainText("trial ends");
    await expect(rows.filter({ hasText: "cus_2" })).toContainText("trial ends");
    await expect(rows.filter({ hasText: "cus_3" })).not.toContainText("trial ends");

    // One click shows the trials.
    await page.getByTestId("trials-ending-show").click();
    await expect(rows.filter({ hasText: "cus_3" })).toHaveCount(0);
    await expect(rows.filter({ hasText: "cus_1" })).toHaveCount(1);

    // The sheet says when, and what happens then.
    await rows.filter({ hasText: "cus_1" }).click();
    await expect(page.getByTestId("trial-banner")).toContainText("in 3 days");
    await expect(page.getByTestId("trial-banner")).toContainText("No card is needed until then");
  });

  test("with no trial ending soon there is no reminder", async ({ page }) => {
    await trialGateway([sub("s2", "cus_2", { state: "SUBSCRIPTION_STATE_TRIALING", trial_end_time: inDays(40) }), sub("s3", "cus_3")]).install(page);
    await openTenantPage(page, "/subscriptions");
    await expect(page.getByRole("row", { name: /cus_2/ })).toBeVisible();
    await expect(page.getByTestId("trials-ending")).toHaveCount(0);
  });
});

test.describe("trials and free tiers on a package", () => {
  function packageGateway() {
    const made: { plans: Record<string, unknown>[]; versions: Record<string, Record<string, unknown>> } = { plans: [], versions: {} };
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/admin/v1/tenants") return { body: { tenants: [{ id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", currency: "TRY", timezone: "Europe/Istanbul" }] } };
      if (c.path === "/v1/meters") return { body: { meters: [{ code: "api_calls", aggregation: "AGGREGATION_COUNT", unit_label: "calls", quantity_scale: 0, version: 1 }] } };
      if (c.path === "/v1/plans" && c.method === "GET") return { body: { plans: made.plans } };
      if (c.path === "/v1/plans" && c.method === "POST") {
        made.plans.push({ id: `p${made.plans.length}`, ...c.body, latest_version: 1 });
        return { status: 201, body: made.plans.at(-1) };
      }
      const v = /^\/v1\/plans\/([^/]+)\/versions(\/latest)?$/.exec(c.path);
      if (v && c.method === "POST") {
        made.versions[v[1] ?? ""] = { version: 1, publish_time: "2026-10-20T10:00:00Z", ...c.body };
        return { status: 201, body: made.versions[v[1] ?? ""] };
      }
      if (v) return { body: made.versions[v[1] ?? ""] ?? {} };
      return undefined;
    });
    return gw;
  }

  test("a package can carry a default trial, and a free tier has no fee and needs a limit", async ({ page }) => {
    const gw = packageGateway();
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });

    // A package with a fee and a 14-day trial.
    await form.getByLabel("Code").fill("team");
    await form.getByLabel("Name", { exact: true }).fill("Team");
    await form.getByLabel("Fixed fee per month (TRY)").fill("99");
    await form.getByLabel("Free trial in days (optional)").fill("14");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect(page.getByRole("row", { name: /Team/ })).toContainText("14-day trial");
    expect(gw.called("POST", "/v1/plans/team/versions")[0]?.body).toMatchObject({ base_fee_minor: "9900", trial_days: 14 });

    // A bad trial is refused before anything is sent.
    await page.getByTestId("packages-new").click();
    await form.getByLabel("Code").fill("bad");
    await form.getByLabel("Name", { exact: true }).fill("Bad");
    await form.getByLabel("Free trial in days (optional)").fill("400");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect(form.getByRole("alert")).toContainText("The trial is a whole number of days from 0 to 365.");
    expect(gw.called("POST", "/v1/plans/bad/versions")).toHaveLength(0);

    // A free tier: no fee, and no limit is no package.
    await form.getByLabel("Free trial in days (optional)").fill("");
    await form.getByTestId("package-free-tier").click();
    await expect(form.getByLabel("Fixed fee per month (TRY)")).toBeDisabled();
    await form.getByLabel("Code").fill("free");
    await form.getByLabel("Name", { exact: true }).fill("Free");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect(form.getByRole("alert")).toContainText("A free tier needs at least one limit.");
    expect(gw.called("POST", "/v1/plans/free/versions")).toHaveLength(0);

    await form.getByRole("button", { name: "Add a quota" }).click();
    await form.getByLabel("Meter of quota 1").selectOption("api_calls");
    await form.getByLabel("Limit of quota 1").fill("1000");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect(page.getByRole("row", { name: /Free/ })).toContainText("Free tier");
    const body = gw.called("POST", "/v1/plans/free/versions")[0]?.body as Record<string, unknown>;
    expect(body).toMatchObject({ base_fee_minor: "0" });
    expect(body).not.toHaveProperty("trial_days");
    expect((body.entitlements as { meter_code: string; limit: string }[])[0]).toMatchObject({ meter_code: "api_calls" });
    expect(body.prices).toBeUndefined();

    // The sheet of the package with the trial says so.
    await page.getByRole("row", { name: /Team/ }).click();
    await expect(page.getByTestId("package-detail")).toContainText("14 days");
  });
});
