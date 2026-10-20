import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, OPERATOR, openTenantPage, TENANT } from "./gateway";

const customer = (id: string, extra: Record<string, unknown> = {}) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com`, ...extra });

test("the console speaks the tenant API as the operator, naming the tenant", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" && c.method === "GET" ? { body: { customers: [customer("cus_1")] } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await expect(page.getByRole("cell", { name: "cus_1", exact: true })).toBeVisible();
  const call = gw.called("GET", "/v1/customers")[0];
  expect(call?.headers["x-quotify-tenant"]).toBe(TENANT);
  expect(call?.headers["authorization"]).toBe(`Bearer ${OPERATOR}`);
});

test.describe("customers", () => {
  test("a customer is created with a country and a billing currency", async ({ page }) => {
    const customers = [customer("cus_1")];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/customers") return undefined;
      if (c.method === "POST") {
        customers.push(customer(String(c.body?.external_id), { country: c.body?.country, billing_currency: c.body?.billing_currency }));
        return { status: 201, body: customers.at(-1) };
      }
      return { body: { customers } };
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_de");
    await form.getByLabel("Name", { exact: true }).fill("Hans");
    await form.getByLabel("E-mail").fill("hans@example.com");
    await form.getByLabel("Country", { exact: true }).fill("de");
    await form.getByLabel("Billing currency").selectOption("EUR");
    await form.getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByRole("cell", { name: "cus_de", exact: true })).toBeVisible();
    expect(gw.called("POST", "/v1/customers")[0]?.body).toEqual({
      external_id: "cus_de",
      name: "Hans",
      email: "hans@example.com",
      country: "DE",
      billing_currency: "EUR",
    });
  });

  test("a refusal from the API is shown", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/customers") return undefined;
      return c.method === "POST" ? { status: 409, body: { error: { code: "ALREADY_EXISTS", message: "customer already exists" } } } : { body: { customers: [] } };
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_1");
    await form.getByLabel("Name", { exact: true }).fill("Ada");
    await form.getByLabel("E-mail").fill("ada@example.com");
    await form.getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByRole("alert")).toContainText("customer already exists");
  });
});

test.describe("packages", () => {
  const plan = { id: "p1", code: "pro", name: "Pro", latest_version: 2 };
  const version = { id: "v2", plan_code: "pro", version: 2, base_fee_minor: "9900", fees: [{ currency: "USD", base_fee_minor: "300" }], prices: [{ meter_code: "calls" }] };

  test("packages are listed with their latest fixed fees", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/plans") return { body: { plans: [plan] } };
      if (c.path === "/v1/plans/pro/versions/latest") return { body: version };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    const row = page.getByRole("row", { name: /pro/ });
    await expect(row).toContainText("v2");
    await expect(row).toContainText("99.00");
    await expect(row).toContainText("3.00");
  });

  test("a version is published with amounts in minor units, one fee per currency", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "POST" && c.path === "/v1/plans") return { status: 201, body: { code: "team", name: "Team" } };
      if (c.method === "POST" && c.path === "/v1/plans/team/versions") return { status: 201, body: { version: 1 } };
      if (c.path === "/v1/plans") return { body: { plans: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("team");
    await form.getByLabel("Name", { exact: true }).fill("Team");
    await form.getByLabel("Fixed fee per month (TRY)").fill("12,50");
    await form.getByRole("button", { name: "Add a fee in another currency" }).click();
    await form.getByLabel("Currency of the extra fee").selectOption("USD");
    await form.getByLabel("Billing interval of the extra fee").selectOption("month");
    await form.getByLabel("Fixed fee 1").fill("3");
    await form.getByRole("button", { name: "Add a fee in another currency" }).click();
    await form.getByLabel("Currency of the extra fee").nth(1).selectOption("TRY");
    await form.getByLabel("Fixed fee 2").fill("120");
    await form.getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/team/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/plans")[0]?.body).toEqual({ code: "team", name: "Team" });
    expect(gw.called("POST", "/v1/plans/team/versions")[0]?.body).toEqual({
      base_fee_minor: "1250",
      fees: [
        { currency: "USD", base_fee_minor: "300", interval: "SUBSCRIPTION_INTERVAL_MONTH" },
        { currency: "TRY", base_fee_minor: "12000", interval: "SUBSCRIPTION_INTERVAL_YEAR" },
      ],
    });
  });

  test("a version of a package that exists is published without creating it again", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "POST" && c.path === "/v1/plans") return { status: 409, body: { error: { code: "ALREADY_EXISTS", message: "plan already exists" } } };
      if (c.method === "POST" && c.path === "/v1/plans/pro/versions") return { status: 201, body: { version: 3 } };
      if (c.path === "/v1/plans") return { body: { plans: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("pro");
    await form.getByLabel("Name", { exact: true }).fill("Pro");
    await form.getByLabel("Fixed fee per month (TRY)").fill("100");
    await form.getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/pro/versions").length).toBe(1);
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("an amount with too many decimals is refused before anything is sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/plans" ? { body: { plans: [] } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/packages");
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("team");
    await form.getByLabel("Name", { exact: true }).fill("Team");
    await form.getByLabel("Fixed fee per month (TRY)").fill("1.005");
    await form.getByRole("button", { name: "Publish version" }).click();
    await expect(page.getByRole("alert")).toContainText("Not an amount in TRY");
    expect(gw.called("POST", "/v1/plans")).toHaveLength(0);
  });
});

test.describe("subscriptions", () => {
  const sub = (id: string, extra: Record<string, unknown> = {}) => ({
    id,
    customer_external_id: "cus_1",
    plan_code: "basic",
    plan_version: 1,
    state: "SUBSCRIPTION_STATE_ACTIVE",
    currency: "TRY",
    owner: "quotify",
    start_time: "2026-10-01T00:00:00Z",
    ...extra,
  });
  const common = (gw: MockGateway, subs: Record<string, unknown>[]) =>
    baseTenant(gw).on((c) => {
      if (c.method === "GET" && c.path === "/v1/subscriptions") return { body: { subscriptions: subs } };
      if (c.path === "/v1/customers") return { body: { customers: [customer("cus_1"), customer("cus_2")] } };
      if (c.path === "/v1/plans") {
        return {
          body: {
            plans: [
              { id: "p1", code: "basic", name: "Basic", latest_version: 1 },
              { id: "p2", code: "pro", name: "Pro", latest_version: 1 },
            ],
          },
        };
      }
      if (c.path.startsWith("/v1/plans/") && c.path.endsWith("/versions/latest")) return { body: { version: 1, base_fee_minor: "1000" } };
      return undefined;
    });

  test("a subscription's panel shows the customer's usage against the package's limits", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1")]).on((c) =>
      c.path === "/v1/customers/cus_1/entitlements"
        ? {
            body: {
              items: [
                { customer_external_id: "cus_1", meter_code: "api_calls", used: "750.000", limit: "1000.000", unlimited: false, allowed: true },
                { customer_external_id: "cus_1", meter_code: "gb_hours", used: "12.500", limit: "10.000", unlimited: false, allowed: false },
                { customer_external_id: "cus_1", meter_code: "seats", used: "3", unlimited: true, allowed: true },
              ],
            },
          }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const usage = page.getByRole("region", { name: "Usage this period" });
    const calls = usage.getByRole("row", { name: /api_calls/ });
    await expect(calls).toContainText("750.000");
    await expect(calls).toContainText("1000.000");
    await expect(calls.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "75");
    await expect(usage.getByRole("row", { name: /gb_hours/ })).toContainText("over the limit");
    await expect(usage.getByRole("row", { name: /seats/ })).toContainText("unlimited");
    expect(gw.called("GET", "/v1/customers/cus_1/entitlements")[0]?.headers["x-quotify-tenant"]).toBe(TENANT);
  });

  test("a subscription is created for a customer and a package, with an optional trial and provider", async ({ page }) => {
    const gw = common(new MockGateway(), []).on((c) => (c.method === "POST" && c.path === "/v1/subscriptions" ? { status: 201, body: sub("s9") } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    const form = page.getByRole("form", { name: "new subscription" });
    await form.getByLabel("Customer").selectOption("cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await form.getByLabel("Billing interval").selectOption("SUBSCRIPTION_INTERVAL_YEAR");
    await form.getByLabel("Trial days").fill("14");
    await form.getByLabel("Provider").selectOption("iyzico");
    await form.getByRole("button", { name: "Create subscription" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toEqual({ customer_external_id: "cus_2", plan_code: "pro", trial_days: 14, provider: "iyzico", interval: "SUBSCRIPTION_INTERVAL_YEAR" });
  });

  test("a coupon is redeemed with a new subscription, and put on or taken off one that exists", async ({ page }) => {
    let withCoupon = false;
    const gw = common(new MockGateway(), []).on((c) => {
      if (c.method === "GET" && c.path === "/v1/subscriptions") {
        return { body: { subscriptions: [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE", coupon_code: withCoupon ? "WELCOME" : undefined })] } };
      }
      if (c.method === "POST" && c.path === "/v1/subscriptions") return { status: 201, body: sub("s9") };
      if (c.method === "POST" && c.path === "/v1/subscriptions/s1/coupon") {
        withCoupon = true;
        return { body: sub("s1") };
      }
      if (c.method === "DELETE" && c.path === "/v1/subscriptions/s1/coupon") {
        withCoupon = false;
        return { body: sub("s1") };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    const form = page.getByRole("form", { name: "new subscription" });
    await form.getByLabel("Customer").selectOption("cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await form.getByLabel("Coupon code").fill("WELCOME");
    await form.getByRole("button", { name: "Create subscription" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toMatchObject({ customer_external_id: "cus_2", plan_code: "pro", coupon_code: "WELCOME" });
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await panel.getByRole("group", { name: "Coupon" }).getByLabel("Coupon code").fill("WELCOME");
    await panel.getByRole("button", { name: "Apply coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/coupon").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions/s1/coupon")[0]?.body).toEqual({ code: "WELCOME" });
    await panel.getByRole("button", { name: "Remove coupon" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1/coupon").length).toBe(1);
  });

  test("a Quotify-run subscription can be paused, moved to another package and cancelled at the period end", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1")]).on((c) => {
      if (c.method === "POST" && /^\/v1\/subscriptions\/s1\/(pause|cancel-at-period-end|change-plan)$/.test(c.path)) return { body: sub("s1") };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await expect(page.getByRole("row", { name: /cus_1/ })).toContainText("basic v1");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await panel.getByRole("button", { name: "Pause" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/pause").length).toBe(1);
    await panel.getByRole("button", { name: "Cancel at period end" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/cancel-at-period-end").length).toBe(1);
    await panel.getByLabel("Change to package").selectOption("pro");
    await panel.getByRole("button", { name: "Change package" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/change-plan").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions/s1/change-plan")[0]?.body).toEqual({ plan_code: "pro" });
  });

  test("a subscription billed in advance is changed with a preview first; a scheduled change can be withdrawn", async ({ page }) => {
    const quote = {
      timing: "CHANGE_TIMING_IMMEDIATELY",
      proration: "CHANGE_PRORATION_PRORATE",
      effective_time: "2026-10-16T00:00:00Z",
      credit_minor: "1600",
      charge_minor: "4800",
      net_minor: "3200",
      currency: "TRY",
      next_renewal_time: "2026-11-01T00:00:00Z",
      next_renewal_minor: "9300",
    };
    let scheduled: Record<string, unknown> | undefined = { plan_code: "basic", plan_version: 1, interval: "SUBSCRIPTION_INTERVAL_MONTH", effective_time: "2026-11-01T00:00:00Z" };
    const gw = common(new MockGateway(), []).on((c) => {
      if (c.method === "GET" && c.path === "/v1/subscriptions") {
        return { body: { subscriptions: [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE", interval: "SUBSCRIPTION_INTERVAL_MONTH", scheduled_change: scheduled })] } };
      }
      if (c.method === "POST" && c.path === "/v1/subscriptions/s1/change-preview") return { body: quote };
      if (c.method === "POST" && c.path === "/v1/subscriptions/s1/change") return { body: { subscription: sub("s2"), quote } };
      if (c.method === "DELETE" && c.path === "/v1/subscriptions/s1/scheduled-change") {
        scheduled = undefined;
        return { body: sub("s1", { fee_timing: "FEE_TIMING_ADVANCE" }) };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Change plan or interval" });
    await expect(panel).toContainText("Scheduled: basic (Monthly) from");
    await expect(panel.getByRole("button", { name: "Apply change" })).toBeDisabled();
    await panel.getByLabel("New package").selectOption("pro");
    await panel.getByRole("button", { name: "Preview" }).click();
    const result = panel.getByLabel("What this change costs");
    await expect(result).toContainText("16.00");
    await expect(result).toContainText("48.00");
    await expect(result).toContainText("32.00 is invoiced now");
    await expect(result).toContainText("93.00");
    // Only what was chosen is sent; the rest is the server's default for the direction of the change.
    expect(gw.called("POST", "/v1/subscriptions/s1/change-preview")[0]?.body).toEqual({ plan_code: "pro" });
    await panel.getByRole("button", { name: "Apply change" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/change").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions/s1/change")[0]?.body).toEqual({ plan_code: "pro" });
    await panel.getByRole("button", { name: "Withdraw" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1/scheduled-change").length).toBe(1);
    await expect(panel).not.toContainText("Scheduled:");
  });

  test("a subscription a provider runs is changed, paused and given a payment link through the provider", async ({ page }) => {
    const base = `/admin/v1/tenants/${TENANT}/providers/paddle/subscriptions/s1`;
    const gw = common(new MockGateway(), [sub("s1", { provider: "paddle", provider_ref: "sub_9", owner: "provider", currency: "USD" })]).on((c) => {
      if (c.method === "POST" && c.path === `${base}/change-preview`) {
        return { body: { currency: "USD", credit_minor: "300", charge_minor: "900", net_minor: "600", next_renewal_minor: "2900", by_provider: true } };
      }
      if (c.method === "POST" && c.path === `${base}/change`) return { body: { changed_at_provider: true, followed: true } };
      if (c.method === "POST" && c.path === `${base}/pause`) return { status: 202, body: { requested: true } };
      if (c.method === "POST" && c.path === `${base}/payment-method-link`) return { body: { url: "https://pay.example/update" } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Run by paddle" });
    await panel.getByLabel("New package").selectOption("pro");
    await panel.getByLabel("New interval").selectOption("SUBSCRIPTION_INTERVAL_YEAR");
    await panel.getByRole("button", { name: "Preview" }).click();
    const quote = panel.getByLabel("What this change costs");
    await expect(quote).toContainText("$6.00 is invoiced now");
    await expect(quote).toContainText("$29.00");
    expect(gw.called("POST", `${base}/change-preview`)[0]?.query.get("environment")).toBe("live");
    expect(gw.called("POST", `${base}/change-preview`)[0]?.body).toEqual({ plan_code: "pro", interval: "SUBSCRIPTION_INTERVAL_YEAR" });
    await panel.getByRole("button", { name: "Apply change" }).click();
    await expect(panel).toContainText("The provider applied the change.");
    await panel.getByRole("button", { name: "Pause" }).click();
    await expect.poll(() => gw.called("POST", `${base}/pause`).length).toBe(1);
    expect(gw.called("POST", `${base}/pause`)[0]?.body).toEqual({ at_period_end: true });
    await panel.getByRole("button", { name: "Payment method link" }).click();
    await expect(panel.getByRole("link", { name: "https://pay.example/update" })).toBeVisible();
  });

  test("a subscription a provider runs cannot be changed here, only moved", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s2", { provider: "paddle", provider_ref: "sub_01", owner: "provider", currency: "USD" })]);
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await expect(page.getByRole("row", { name: /cus_1/ })).toContainText("run by provider");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await expect(panel).toContainText("paddle runs this subscription");
    await expect(panel.getByRole("button", { name: "Change package" })).toHaveCount(0);
    await expect(panel.getByRole("region", { name: "Run by paddle" })).toBeVisible();
    await expect(panel.getByLabel("Move to provider")).toBeVisible();
  });

  test("moving to a provider that runs subscriptions shows its checkout; the old subscription is not ended here", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1", { provider: "iyzico", currency: "USD" })]).on((c) =>
      c.method === "POST" && c.path === `/admin/v1/tenants/${TENANT}/provider-migrations`
        ? { status: 201, body: { migration: "m1", state: "started", checkout_url: "https://pay.example/co" } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await panel.getByLabel("Move to provider").selectOption("paddle");
    await expect(panel.getByRole("button", { name: "Move", exact: true })).toBeDisabled();
    await panel.getByLabel("Price at the provider (pri_…)").fill("pri_01");
    await panel.getByRole("button", { name: "Move", exact: true }).click();
    await expect(panel.getByRole("link", { name: "https://pay.example/co" })).toBeVisible();
    const call = gw.called("POST", `/admin/v1/tenants/${TENANT}/provider-migrations`)[0];
    expect(call?.query.get("environment")).toBe("live");
    expect(call?.body).toEqual({ subscription_id: "s1", to_provider: "paddle", price_ref: "pri_01" });
    expect(call?.headers["x-quotify-tenant"]).toBeUndefined();
  });

  test("ending a prepaid subscription can credit the unused part", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE" }), sub("s2", { fee_timing: "FEE_TIMING_ADVANCE", coupon_code: "TEN" })]).on((c) =>
      c.method === "DELETE" && c.path === "/v1/subscriptions/s1" ? { body: { subscription: sub("s1"), credited_minor: "1999" } } : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).first().click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    page.once("dialog", (d) => void d.accept());
    await panel.getByRole("button", { name: "End now and credit the unused part" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1").length).toBe(1);
    expect(gw.called("DELETE", "/v1/subscriptions/s1")[0]?.query.get("credit_unused")).toBe("true");
    await expect(panel.getByRole("status")).toContainText("19.99");
    // A subscription with a coupon is left to a credit note.
    await page.getByRole("button", { name: "Manage" }).nth(1).click();
    await expect(page.getByRole("button", { name: "End now and credit the unused part" })).toHaveCount(0);
  });

  test("ending a subscription asks first", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1")]).on((c) => (c.method === "DELETE" && c.path === "/v1/subscriptions/s1" ? { body: sub("s1") } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    page.once("dialog", (d) => void d.dismiss());
    await panel.getByRole("button", { name: "End now" }).click();
    expect(gw.called("DELETE", "/v1/subscriptions/s1")).toHaveLength(0);
    page.once("dialog", (d) => void d.accept());
    await panel.getByRole("button", { name: "End now" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1").length).toBe(1);
  });
});

test.describe("pricing", () => {
  const quiet = (c: { path: string; method: string }) => {
    if (c.path === "/v1/coupons" && c.method === "GET") return { body: { coupons: [] } };
    if (c.path === "/v1/tax-rates" && c.method === "GET") return { body: { rates: [{ country: "DE", name: "VAT", rate_bps: 1900 }, { country: "", name: "Sales tax", rate_bps: 825 }] } };
    if (c.path === "/v1/dunning-settings" && c.method === "GET") return { body: { retry_hours: [24, 72, 168], defaults_in_use: true } };
    return undefined;
  };

  test("payment retries are listed in hours and saved as the whole list", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/dunning-settings" && c.method === "PUT") return { body: { retry_hours: (c.body as { retry_hours: number[] }).retry_hours } };
      return quiet(c);
    });
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    await expect(page.getByLabel("Hours before retry 2")).toHaveValue("72");
    await page.getByLabel("Hours before retry 3").fill("0");
    await page.getByRole("button", { name: "Save retries" }).click();
    await expect(page.getByRole("alert")).toContainText("whole number from 1 to 720");
    expect(gw.called("PUT", "/v1/dunning-settings")).toHaveLength(0);
    await page.getByLabel("Hours before retry 3").fill("120");
    await page.getByRole("button", { name: "Save retries" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/dunning-settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/dunning-settings")[0]?.body).toEqual({ retry_hours: [24, 72, 120] });
  });

  test("a coupon is made as a percentage or an amount, and deactivated", async ({ page }) => {
    const coupons: Record<string, unknown>[] = [];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/coupons" && c.method === "POST") {
        coupons.push({ ...c.body, redeemed: 0, active: true });
        return { status: 201, body: coupons.at(-1) };
      }
      if (c.path === "/v1/coupons" && c.method === "GET") return { body: { coupons } };
      if (c.method === "DELETE" && c.path === "/v1/coupons/WELCOME") {
        coupons[0] = { ...coupons[0], active: false };
        return { body: coupons[0] };
      }
      return quiet(c);
    });
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    const form = page.getByRole("form", { name: "new coupon" });
    await form.getByLabel("Code").fill("WELCOME");
    await form.getByLabel("Value (10 or 12.5 / 4.99)").fill("12,5");
    await form.getByLabel("Duration").selectOption("COUPON_DURATION_REPEATING");
    await form.getByLabel("Fees", { exact: true }).fill("3");
    await form.getByLabel("Redemptions limit").fill("100");
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons").length).toBe(1);
    expect(gw.called("POST", "/v1/coupons")[0]?.body).toEqual({
      code: "WELCOME",
      name: "",
      duration: "COUPON_DURATION_REPEATING",
      percent_bps: 1250,
      duration_periods: 3,
      max_redemptions: 100,
    });
    const row = page.getByRole("row", { name: /WELCOME/ });
    await expect(row).toContainText("12.5 %");
    await expect(row).toContainText("First 3 fees");
    await expect(row).toContainText("0 / 100");
    await form.getByLabel("Code").fill("MINUS5");
    await form.getByLabel("Kind").selectOption("amount");
    await form.getByLabel("Value (10 or 12.5 / 4.99)").fill("4.99");
    await form.getByLabel("Currency").selectOption("USD");
    await form.getByLabel("Duration").selectOption("COUPON_DURATION_FOREVER");
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect.poll(() => gw.called("POST", "/v1/coupons").length).toBe(2);
    expect(gw.called("POST", "/v1/coupons")[1]?.body).toMatchObject({ code: "MINUS5", amount_minor: "499", currency: "USD", duration: "COUPON_DURATION_FOREVER" });
    await row.getByRole("button", { name: "Deactivate" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/coupons/WELCOME").length).toBe(1);
  });

  test("a percentage that is not one is refused before anything is sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(quiet);
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    const form = page.getByRole("form", { name: "new coupon" });
    await form.getByLabel("Code").fill("BAD");
    await form.getByLabel("Value (10 or 12.5 / 4.99)").fill("101");
    await form.getByRole("button", { name: "Create coupon" }).click();
    await expect(page.getByRole("alert")).toContainText("Not a percentage");
    expect(gw.called("POST", "/v1/coupons")).toHaveLength(0);
  });

  test("tax rates are shown as percentages and saved as basis points, replacing the list", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => (c.method === "PUT" && c.path === "/v1/tax-rates" ? { body: c.body } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    await expect(page.getByLabel("Rate 1 (%)")).toHaveValue("19");
    await expect(page.getByLabel("Rate 2 (%)")).toHaveValue("8.25");
    await page.getByRole("button", { name: "Add a rate" }).click();
    await page.getByLabel("Country 3").fill("tr");
    await page.getByLabel("Name 3").fill("KDV");
    await page.getByLabel("Rate 3 (%)").fill("20");
    await page.getByRole("button", { name: "Save rates" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/tax-rates").length).toBe(1);
    expect(gw.called("PUT", "/v1/tax-rates")[0]?.body).toEqual({
      rates: [
        { country: "DE", name: "VAT", rate_bps: 1900 },
        { country: "", name: "Sales tax", rate_bps: 825 },
        { country: "TR", name: "KDV", rate_bps: 2000 },
      ],
    });
    await page.getByLabel("Rate 1 (%)").fill("19.999");
    await page.getByRole("button", { name: "Save rates" }).click();
    await expect(page.getByRole("alert")).toContainText("Not a percentage");
    expect(gw.called("PUT", "/v1/tax-rates")).toHaveLength(1);
  });
});

test.describe("credit notes", () => {
  const invoice = (status: string) => ({
    id: "inv1",
    number: "INV-2026-000001",
    customer_external_id: "cus_1",
    period_id: "2026-10",
    kind: "REGULAR",
    currency: "USD",
    total_minor: "10000",
    status,
    saga_state: "COMPLETED",
    lines: [
      { line_no: 1, kind: "BASE_FEE", description: "Base fee", amount_minor: "10000", service_from: "2026-10-01T00:00:00Z", service_to: "2026-11-01T00:00:00Z" },
    ],
  });
  const common = (status: string, notes: Record<string, unknown>[]) =>
    baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1`) return { body: invoice(status) };
      if (c.path === `/admin/v1/tenants/${TENANT}/invoices/inv1/saga`) return { body: { state: "COMPLETED", events: [] } };
      if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: notes } };
      return undefined;
    });

  test("a paid invoice is credited: an amount in major units, a reason, and the note is listed", async ({ page }) => {
    const notes: Record<string, unknown>[] = [];
    const gw = common("paid", notes).on((c) => {
      if (c.method === "POST" && c.path === "/admin/v1/invoices/inv1/credit-notes") {
        notes.push({ id: "cn1", number: "CN-2026-000001", invoice_number: "INV-2026-000001", customer_external_id: "cus_1", currency: "USD", total_minor: c.body?.amount_minor, reason: c.body?.reason, create_time: "2026-10-20T10:00:00Z" });
        return { status: 201, body: notes[0] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByRole("row", { name: /Base fee/ })).toContainText("Oct 1, 2026");
    const form = page.getByRole("form", { name: "new credit note" });
    await form.getByLabel("Amount").fill("25.50");
    await form.getByLabel("Reason").fill("goodwill");
    await form.getByRole("button", { name: "Issue credit note" }).click();
    await expect.poll(() => gw.called("POST", "/admin/v1/invoices/inv1/credit-notes").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/invoices/inv1/credit-notes")[0]?.body).toEqual({ tenant_id: TENANT, amount_minor: "2550", reason: "goodwill" });
    await expect(page.getByRole("row", { name: /CN-2026-000001/ })).toContainText("$25.50");
  });

  test("a credit note can be refunded to the card, and a failed refund is retried", async ({ page }) => {
    const notes: Record<string, unknown>[] = [];
    const gw = common("paid", notes).on((c) => {
      if (c.method === "POST" && c.path === "/admin/v1/invoices/inv1/credit-notes") {
        notes.push({ id: "cn1", number: "CN-2026-000001", invoice_number: "INV-2026-000001", customer_external_id: "cus_1", currency: "USD", total_minor: "1000",
          disposition: "CREDIT_NOTE_DISPOSITION_REFUND", refund_state: "REFUND_STATE_FAILED", refund_error: "rejected: card closed", create_time: "2026-10-20T10:00:00Z" });
        return { status: 201, body: notes[0] };
      }
      if (c.method === "POST" && c.path === "/admin/v1/credit-notes/cn1/retry-refund") {
        notes[0] = { ...notes[0], refund_state: "REFUND_STATE_REQUESTED", refund_error: "" };
        return { body: notes[0] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    const form = page.getByRole("form", { name: "new credit note" });
    await form.getByLabel("Amount").fill("10");
    await form.getByLabel("Refund to the card").check();
    await form.getByRole("button", { name: "Issue credit note" }).click();
    await expect.poll(() => gw.called("POST", "/admin/v1/invoices/inv1/credit-notes").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/invoices/inv1/credit-notes")[0]?.body).toEqual({ tenant_id: TENANT, amount_minor: "1000", reason: "", refund_to_card: true });
    const row = page.getByRole("row", { name: /CN-2026-000001/ });
    await expect(row).toContainText("Refund failed");
    await expect(row).toContainText("card closed");
    await row.getByRole("button", { name: "Retry refund" }).click();
    await expect.poll(() => gw.called("POST", "/admin/v1/credit-notes/cn1/retry-refund").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/retry-refund")[0]?.body).toEqual({ tenant_id: TENANT });
    await expect(row).toContainText("Refund requested");
  });

  test("an invoice that is not paid cannot be credited here, and an empty amount credits what is left", async ({ page }) => {
    const gw = common("open", []);
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByRole("form", { name: "new credit note" })).toHaveCount(0);
    await expect(page.getByText("No credit notes.")).toBeVisible();

    const paid = common("paid", []).on((c) => (c.method === "POST" && c.path === "/admin/v1/invoices/inv1/credit-notes" ? { status: 201, body: { id: "cn" } } : undefined));
    const page2 = await page.context().newPage();
    await paid.install(page2);
    await openTenantPage(page2, "/invoices/inv1");
    await page2.getByRole("form", { name: "new credit note" }).getByRole("button", { name: "Issue credit note" }).click();
    await expect.poll(() => paid.called("POST", "/admin/v1/invoices/inv1/credit-notes").length).toBe(1);
    expect(paid.called("POST", "/admin/v1/invoices/inv1/credit-notes")[0]?.body).toEqual({ tenant_id: TENANT, amount_minor: "0", reason: "" });
  });
});

test.describe("providers", () => {
  const quiet = (c: { path: string; method: string }) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-accounts` && c.method === "GET") return { body: { accounts: [] } };
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-subscriptions`) return { body: { subscriptions: [] } };
    if (c.path === "/v1/routing-rules" && c.method === "GET") return { body: { rules: [] } };
    return undefined;
  };

  test("a package is synced to the provider's catalogue and what exists there is listed", async ({ page }) => {
    const cat = `/admin/v1/tenants/${TENANT}/providers/paddle/catalogue`;
    const objects: Record<string, string>[] = [{ kind: "product", key: "plan:pro", ref: "pro_1" }];
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => {
        if (c.path === "/v1/plans") return { body: { plans: [{ id: "p2", code: "pro", name: "Pro", latest_version: 1 }] } };
        if (c.path.startsWith("/v1/plans/") && c.path.endsWith("/versions/latest")) return { body: { version: 1, base_fee_minor: "1000" } };
        if (c.method === "GET" && c.path === cat) return { body: { objects } };
        if (c.method === "POST" && c.path === `${cat}/sync`) {
          objects.push({ kind: "price", key: "v1|month|USD", ref: "pri_1" });
          return { body: { created: { "v1|month|USD": "pri_1" }, existing: {}, skipped: { "v1|month|TRY": "Paddle cannot bill in TRY" } } };
        }
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/providers");
    const form = page.getByRole("form", { name: "Catalogue at the provider" });
    await form.getByLabel("Provider").selectOption("paddle");
    await expect(page.getByRole("row", { name: /plan:pro/ })).toContainText("pro_1");
    await form.getByLabel("Package", { exact: true }).selectOption("pro");
    await form.getByRole("button", { name: "Sync package" }).click();
    await expect.poll(() => gw.called("POST", cat + "/sync").length).toBe(1);
    expect(gw.called("POST", cat + "/sync")[0]?.body).toEqual({ plan_code: "pro" });
    expect(gw.called("POST", cat + "/sync")[0]?.query.get("environment")).toBe("live");
    await expect(page.getByRole("status").filter({ hasText: "1 price(s) created" })).toContainText("v1|month|TRY: Paddle cannot bill in TRY");
    await expect(page.getByRole("row", { name: /v1\|month\|USD/ })).toContainText("pri_1");
  });

  test("an account's settings are validated as JSON, sent with the environment, and never shown again", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => {
        if (c.path === `/admin/v1/tenants/${TENANT}/provider-accounts` && c.method === "GET") {
          return { body: { accounts: [{ provider: "iyzico", environment: "live", is_default: true, updated_at: "2026-10-20T10:00:00Z" }] } };
        }
        if (c.method === "PUT" && c.path === `/admin/v1/tenants/${TENANT}/provider-accounts/paddle`) return { body: { provider: "paddle" } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/providers");
    await expect(page.getByRole("row", { name: /iyzico/ })).toContainText("live");
    const form = page.getByRole("form", { name: "Add or replace an account" });
    await form.getByLabel("Provider").selectOption("paddle");
    const settings = form.getByLabel("Settings (JSON)");
    await expect(settings).toHaveValue(/webhook_secret/);
    await expect(form).toContainText(`/webhooks/paddle/${TENANT}/live`);
    await settings.fill("{ not json");
    await form.getByRole("button", { name: "Save account" }).click();
    await expect(page.getByRole("alert")).toContainText("not valid JSON");
    expect(gw.called("PUT", /provider-accounts/)).toHaveLength(0);
    await settings.fill('{"api_key":"k","webhook_secret":"s","usage_product_id":"pro_1"}');
    await form.getByRole("button", { name: "Save account" }).click();
    await expect.poll(() => gw.called("PUT", /provider-accounts\/paddle/).length).toBe(1);
    const put = gw.called("PUT", /provider-accounts\/paddle/)[0];
    expect(put?.query.get("environment")).toBe("live");
    expect(put?.query.get("default")).toBe("true");
    expect(put?.body).toEqual({ api_key: "k", webhook_secret: "s", usage_product_id: "pro_1" });
    await expect(settings).not.toHaveValue(/"k"/);
  });

  test("routing rules are edited, saved in order, and previewed", async ({ page }) => {
    let rules = [{ country: "TR", currency: "TRY", provider: "iyzico" }];
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => {
        if (c.path === "/v1/routing-rules" && c.method === "GET") return { body: { rules } };
        if (c.path === "/v1/routing-rules" && c.method === "PUT") {
          rules = (c.body?.rules ?? []) as typeof rules;
          return { body: { rules } };
        }
        if (c.path === "/v1/routing-rules/preview") return { body: { provider: "paddle", rule_index: 1, owner: "provider", can_charge: true } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/providers");
    await expect(page.getByLabel("Country of rule 1")).toHaveValue("TR");
    await page.getByRole("button", { name: "Add a rule" }).click();
    await page.getByLabel("Currency of rule 2").selectOption("USD");
    await page.getByLabel("Provider of rule 2").selectOption("paddle");
    await page.getByRole("button", { name: "Save rules" }).click();
    await expect(page.getByText("Rules saved.")).toBeVisible();
    expect(gw.called("PUT", "/v1/routing-rules")[0]?.body).toEqual({
      rules: [
        { country: "TR", currency: "TRY", provider: "iyzico" },
        { country: "", currency: "USD", provider: "paddle" },
      ],
    });
    const preview = page.getByRole("form", { name: "Where would a customer go?" });
    await preview.getByLabel("Country").fill("de");
    await preview.getByLabel("Currency").selectOption("USD");
    await preview.getByRole("button", { name: "Preview" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Goes to paddle (rule 2)." })).toBeVisible();
    expect(gw.called("POST", "/v1/routing-rules/preview")[0]?.body).toEqual({ country: "DE", currency: "USD" });
  });

  test("a rule the API refuses shows why", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => {
        if (c.path === "/v1/routing-rules" && c.method === "PUT") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", message: "rules[0].provider: paddle cannot charge in TRY" } } };
        if (c.path === "/v1/routing-rules" && c.method === "GET") return { body: { rules: [{ country: "", currency: "TRY", provider: "paddle" }] } };
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/providers");
    await page.getByRole("button", { name: "Save rules" }).click();
    await expect(page.getByRole("alert")).toContainText("paddle cannot charge in TRY");
  });
});

test.describe("payments", () => {
  test("the ledger is listed and a reconciliation names what does not agree", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) {
        return {
          body: {
            entries: [
              { id: 1, provider: "iyzico", environment: "live", kind: "charge", status: "succeeded", amount_minor: 1116, currency: "TRY", provider_ref: "P1", source: "command", occurred_at: "2026-10-20T10:00:00Z" },
            ],
          },
        };
      }
      if (c.path === `/admin/v1/tenants/${TENANT}/reconciliation`) {
        return {
          body: {
            period: c.query.get("period"),
            paid_invoices: 3,
            successful_charges: 2,
            discrepancy_counts: { paid_without_charge: 1, amount_mismatch: 1 },
            discrepancies: [
              { kind: "paid_without_charge", invoice_id: "inv-1", number: "INV-2026-000001", currency: "TRY", billed_minor: 500 },
              { kind: "amount_mismatch", invoice_id: "inv-2", number: "INV-2026-000002", provider: "iyzico", provider_ref: "P2", currency: "TRY", billed_minor: 700, ledger_minor: 650 },
            ],
          },
        };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    const entry = page.getByRole("row", { name: /iyzico/ });
    await expect(entry).toContainText("11.16");
    await expect(entry).toContainText("succeeded");
    const form = page.getByRole("form", { name: "Reconcile with the ledger" });
    await form.getByLabel("Period (YYYY-MM)").fill("2026-10");
    await form.getByRole("button", { name: "Reconcile" }).click();
    const result = page.getByRole("region", { name: "reconciliation result" });
    await expect(result).toContainText("3 paid invoice(s) and 2 successful charge(s)");
    await expect(result.getByText("Paid, no charge in the ledger: 1")).toBeVisible();
    await expect(result.getByRole("row", { name: /INV-2026-000002/ })).toContainText("6.50");
    expect(gw.called("GET", `/admin/v1/tenants/${TENANT}/reconciliation`)[0]?.query.get("period")).toBe("2026-10");
  });

  test("a clean reconciliation says so", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) return { body: { entries: [] } };
      if (c.path === `/admin/v1/tenants/${TENANT}/reconciliation`) return { body: { period: "2026-10", paid_invoices: 0, successful_charges: 0, discrepancy_counts: {}, discrepancies: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    await page.getByRole("form", { name: "Reconcile with the ledger" }).getByRole("button", { name: "Reconcile" }).click();
    await expect(page.getByText("The invoices and the ledger agree.")).toBeVisible();
    await expect(page.getByText("Nothing in the ledger yet.")).toBeVisible();
  });
});

test.describe("reports", () => {
  test("revenue is a row per currency and follows the chosen split", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path !== "/v1/reports/revenue") return undefined;
      const provider = (c.query.get("group_by") ?? "").includes("provider");
      return {
        body: {
          rows: provider
            ? [{ period: "2026-10", provider: "paddle", country: "", currency: "USD", invoices: "2", billed_minor: "5000", collected_minor: "4000", refunded_minor: "0", outstanding_minor: "1000", uncollectible_minor: "0" }]
            : [
                { period: "2026-10", provider: "", country: "", currency: "TRY", invoices: "3", billed_minor: "150000", collected_minor: "100000", refunded_minor: "0", outstanding_minor: "50000", uncollectible_minor: "0" },
                { period: "2026-10", provider: "", country: "", currency: "USD", invoices: "2", billed_minor: "5000", collected_minor: "4000", refunded_minor: "0", outstanding_minor: "1000", uncollectible_minor: "0" },
              ],
        },
      };
    });
    await gw.install(page);
    await openTenantPage(page, "/reports");
    await expect(page.getByRole("row", { name: /TRY/ })).toContainText("1,500.00");
    await expect(page.getByRole("row", { name: /USD/ })).toContainText("50.00");
    expect(gw.called("GET", "/v1/reports/revenue")[0]?.query.get("group_by")).toBe("period");
    await page.getByRole("checkbox", { name: "Provider" }).click();
    await expect(page.getByRole("columnheader", { name: "Provider" })).toBeVisible();
    await expect(page.getByRole("row", { name: /paddle/ })).toContainText("40.00");
    expect(gw.calls.filter((c) => c.path === "/v1/reports/revenue").at(-1)?.query.get("group_by")).toBe("period,provider");
  });

  test("subscriptions show monthly recurring revenue and the dunning list links to its invoices", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/reports/revenue") return { body: { rows: [] } };
      if (c.path === "/v1/reports/subscriptions") {
        return {
          body: {
            month: "2026-10",
            rows: [{ provider: "", country: "", currency: "TRY", active: "3", trialing: "1", paused: "0", past_due: "1", mrr_minor: "2100000", new_subscriptions: "1", new_mrr_minor: "900000", churned: "0", churned_mrr_minor: "0" }],
          },
        };
      }
      if (c.path === "/v1/reports/dunning") {
        return {
          body: {
            summary: [{ state: "UNCOLLECTIBLE", provider: "paddle", currency: "USD", invoices: "1", total_minor: "300" }],
            invoices: [
              { invoice_id: "inv-9", number: "INV-2026-000009", customer_external_id: "cus_1", period: "2026-10", provider: "paddle", currency: "USD", total_minor: "300", state: "UNCOLLECTIBLE", dunning_attempt: 3 },
            ],
          },
        };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/reports");
    await page.getByRole("tab", { name: "Subscriptions" }).click();
    const row = page.getByRole("row", { name: /TRY/ });
    await expect(row).toContainText("21,000.00");
    await expect(row).toContainText("1 · TRY 9,000.00");
    await page.getByRole("tab", { name: "Unpaid invoices" }).click();
    await expect(page.getByRole("link", { name: "INV-2026-000009" })).toHaveAttribute("href", `/tenants/${TENANT}/invoices/inv-9`);
    await expect(page.getByText("given up").first()).toBeVisible();
  });
});

test("the billing pages are translated", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" ? { body: { customers: [] } } : undefined));
  await gw.install(page);
  await page.goto("/");
  await page.getByLabel("Language").selectOption("tr");
  await page.getByLabel("Operatör jetonu").fill(OPERATOR);
  await page.getByRole("button", { name: "Giriş yap" }).click();
  await page.waitForURL("**/tenants");
  await page.goto(`/tenants/${TENANT}/customers`);
  await expect(page.getByRole("link", { name: "Müşteriler" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Abonelikler" })).toBeVisible();
  await expect(page.getByRole("form", { name: "yeni müşteri" })).toBeVisible();
});

test.describe("webhooks", () => {
  const endpoint = { id: "ep1", url: "https://example.com/hook", description: "main", event_types: ["invoice.paid"], active: true, create_time: "2026-10-20T10:00:00Z" };

  test("an endpoint is added with the events it wants, its secret is shown once, and deliveries can be sent again", async ({ page }) => {
    const endpoints: Record<string, unknown>[] = [];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/webhook-endpoints" && c.method === "GET") return { body: { endpoints } };
      if (c.path === "/v1/webhook-endpoints" && c.method === "POST") {
        endpoints.push({ ...endpoint, url: (c.body as { url: string }).url, event_types: (c.body as { event_types: string[] }).event_types });
        return { status: 201, body: { ...endpoints[0], secret: "whsec_ABC123" } };
      }
      if (c.path === "/v1/webhook-deliveries" && c.method === "GET") {
        return {
          body: {
            deliveries: [
              { id: "d1", event_id: "e1", event_type: "invoice.paid", endpoint_id: "ep1", state: "WEBHOOK_DELIVERY_STATE_FAILED", attempts: 7, last_status: 500, last_error: "HTTP 500: nope", create_time: "2026-10-20T10:00:00Z" },
            ],
          },
        };
      }
      if (c.path === "/v1/webhook-deliveries/d1/redeliver" && c.method === "POST") return { status: 202, body: { id: "d1", state: "WEBHOOK_DELIVERY_STATE_PENDING" } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/webhooks");
    await expect(page.getByText("No webhook endpoints.")).toBeVisible();
    const form = page.getByRole("form", { name: "new webhook endpoint" });
    await form.getByLabel("Endpoint URL").fill("https://example.com/hook");
    await form.getByLabel("invoice.paid").check();
    await form.getByRole("button", { name: "Add endpoint" }).click();
    await expect.poll(() => gw.called("POST", "/v1/webhook-endpoints").length).toBe(1);
    expect(gw.called("POST", "/v1/webhook-endpoints")[0]?.body).toEqual({ url: "https://example.com/hook", description: "", event_types: ["invoice.paid"] });
    await expect(page.getByTestId("webhook-secret")).toHaveText("whsec_ABC123");
    const row = page.getByRole("row", { name: /example.com\/hook/ });
    await expect(row).toContainText("invoice.paid");
    await row.getByRole("button", { name: "Deliveries" }).click();
    const log = page.getByRole("region", { name: "Deliveries" });
    await expect(log).toContainText("Failed");
    await expect(log).toContainText("HTTP 500: nope");
    await log.getByRole("button", { name: "Send again" }).click();
    await expect.poll(() => gw.called("POST", "/v1/webhook-deliveries/d1/redeliver").length).toBe(1);
  });
});
