import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, OPERATOR, OPERATOR_EMAIL, OPERATOR_PASSWORD, openTenantPage, TENANT } from "./gateway";
import { pickCurrency, pickFilter, pickLanguage, pickPlace, confirmDialog, rowAction } from "./ui";

const customer = (id: string, extra: Record<string, unknown> = {}) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com`, create_time: "2026-10-05T10:00:00Z", ...extra });

test("the console speaks the tenant API as the operator user (a session), naming the tenant", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" && c.method === "GET" ? { body: { customers: [customer("cus_1")] } } : undefined));
  await gw.install(page);
  await openTenantPage(page, "/customers");
  await expect(page.getByTestId("customer-cus_1")).toContainText("Name cus_1");
  const call = gw.called("GET", "/v1/customers")[0];
  expect(call?.headers["x-payrudder-tenant"]).toBe(TENANT);
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
    await page.getByTestId("customers-new").click();
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_de");
    await form.getByLabel("Name", { exact: true }).fill("Hans");
    await form.getByLabel("E-mail").fill("hans@example.com");
    await form.getByLabel("Country", { exact: true }).fill("de");
    await pickCurrency(form.getByLabel("Billing currency"), "EUR");
    await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByTestId("customer-cus_de")).toContainText("Name cus_de");
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
    await page.getByTestId("customers-new").click();
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_1");
    await form.getByLabel("Name", { exact: true }).fill("Ada");
    await form.getByLabel("E-mail").fill("ada@example.com");
    await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByRole("alert")).toContainText("customer already exists");
  });
});

test.describe("customer addresses", () => {
  test("an address is entered with a customer and edited later", async ({ page }) => {
    const customers: Record<string, unknown>[] = [customer("cus_1", { country: "TR", address: { line1: "Atatürk Cd. 1", city: "İstanbul", postal_code: "34000", country: "TR" } })];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "PUT" && c.path === "/v1/customers/cus_1/address") {
        customers[0] = { ...customers[0], address: c.body, country: (c.body as { country?: string }).country ?? customers[0]?.country };
        return { body: customers[0] };
      }
      if (c.method === "POST" && c.path === "/v1/customers") {
        customers.push(customer(String(c.body?.external_id), { address: c.body?.address }));
        return { status: 201, body: customers.at(-1) };
      }
      if (c.path === "/v1/customers") return { body: { customers } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-tab-details").click();
    await expect(page.getByTestId("customer-detail")).toContainText("Atatürk Cd. 1, 34000 İstanbul, TR");
    await page.keyboard.press("Escape");

    await page.getByTestId("customers-new").click();
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_2");
    await form.getByLabel("Name").fill("Bob");
    await form.getByLabel("E-mail").fill("bob@example.com");
    await form.getByLabel("Country").fill("de");
    await form.getByLabel("Address line 1").fill(" Hauptstr. 5 ");
    await form.getByLabel("City").fill("Berlin");
    await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
    await expect.poll(() => gw.called("POST", "/v1/customers").length).toBe(1);
    expect(gw.called("POST", "/v1/customers")[0]?.body).toMatchObject({ external_id: "cus_2", country: "DE", address: { line1: "Hauptstr. 5", city: "Berlin", country: "DE" } });

    await page.getByRole("row", { name: /cus_1/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-address").click();
    const edit = page.getByRole("dialog", { name: "Address of cus_1" });
    await edit.getByLabel("City").fill("Ankara");
    await edit.getByRole("button", { name: "Save address" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/address").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_1/address")[0]?.body).toEqual({ line1: "Atatürk Cd. 1", city: "Ankara", postal_code: "34000", country: "TR" });
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-tab-details").click();
    await expect(page.getByTestId("customer-detail")).toContainText("34000 Ankara");
  });
});

test.describe("packages", () => {
  // The packages form defaults to the tenant's currency, which the page reads from the tenant list (the operator's list, or a person's apps).
  const tenantList = (c: { method: string; path: string }) =>
    c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [{ id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", currency: "TRY", timezone: "Europe/Istanbul" }] } } : undefined;
  const plan = { id: "p1", code: "pro", name: "Pro", latest_version: 2 };
  const version = { id: "v2", plan_code: "pro", version: 2, base_fee_minor: "9900", fees: [{ currency: "USD", base_fee_minor: "300" }], prices: [{ meter_code: "calls" }], publish_time: "2026-10-05T10:00:00Z" };

  test("packages are listed with their latest fixed fees", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => {
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

  test("a price change is checked first, then scheduled after asking, and the subscriptions left out are explained", async ({ page }) => {
    const result = (dry: boolean) => ({ scheduled: 3, scheduled_subscription_ids: ["s1", "s2", "s3"], skipped: [{ subscription_id: "s4", customer_external_id: "cus_4", reason: "renews_soon" }, { subscription_id: "s5", customer_external_id: "cus_5", reason: "renews_soon" }, { subscription_id: "s6", customer_external_id: "cus_6", reason: "has_pending_change" }], to_version: 2, notice_days: dry ? 21 : 21, price_change_id: dry ? "" : "pc-new", already_scheduled: dry ? 0 : 2 });
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => {
      if (c.path === "/v1/plans") return { body: { plans: [plan] } };
      if (c.path === "/v1/plans/pro/versions/latest") return { body: version };
      if (c.method === "POST" && c.path === "/v1/plans/pro/price-changes") return { body: result(c.body?.dry_run === true) };
      if (c.method === "GET" && c.path === "/v1/price-changes") return { body: { price_changes: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await rowAction(page.getByRole("row", { name: /pro/ }), "Schedule a price change");
    const sheet = page.getByTestId("price-change-sheet");
    // The notice cannot be shorter than 14 days, and nothing can be scheduled before it is checked.
    await expect(sheet.getByTestId("pc-schedule")).toBeDisabled();
    await sheet.getByTestId("pc-notice").fill("7");
    await expect(sheet.getByTestId("pc-check-button")).toBeDisabled();
    await sheet.getByTestId("pc-notice").fill("21");
    await sheet.getByTestId("pc-check-button").click();
    await expect(sheet.getByTestId("pc-check")).toContainText("3 subscriptions will move to v2");
    await expect(sheet.getByTestId("pc-check")).toContainText("2 renew too soon");
    await expect(sheet.getByTestId("pc-check")).toContainText("1 already have a change waiting");
    expect(gw.called("POST", "/v1/plans/pro/price-changes")[0]?.body).toEqual({ from_version: 1, notice_days: 21, dry_run: true });
    // Changing a field throws the check away.
    await sheet.getByTestId("pc-notice").fill("30");
    await expect(sheet.getByTestId("pc-schedule")).toBeDisabled();
    await sheet.getByTestId("pc-check-button").click();
    await sheet.getByTestId("pc-schedule").click();
    await confirmDialog(page);
    await expect(sheet.getByTestId("pc-done")).toContainText("3 subscriptions scheduled to move to v2");
    await expect(sheet.getByTestId("pc-done")).toContainText("2 were scheduled by this price change before and are not told again");
    expect(gw.called("POST", "/v1/plans/pro/price-changes").at(-1)?.body).toEqual({ from_version: 1, notice_days: 30, dry_run: false });
  });

  test("the price changes made are listed with what became of each subscription; the skipped are tried again and what is waiting is withdrawn after asking", async ({ page }) => {
    const change = (id: string, extra: Record<string, unknown>): Record<string, unknown> => ({ id, plan_code: "pro", from_version: 1, to_version: 2, notice_days: 30, effective_time: "2026-12-05T10:00:00Z", state: "open", created_by: "boss@example.com",
      create_time: "2026-11-02T09:00:00Z", withdraw_time: null, withdrawn_by: "", scheduled: 3, applied: 0, withdrawn: 0, skipped: 2, ...extra });
    let open = change("pc-1", {});
    const older = change("pc-0", { state: "withdrawn", withdraw_time: "2026-10-20T09:00:00Z", withdrawn_by: "boss@example.com", scheduled: 0, applied: 4, withdrawn: 1, skipped: 0, create_time: "2026-10-01T09:00:00Z" });
    const items = [
      { subscription_id: "s1", customer_external_id: "cus_a", state: "scheduled", reason: "", effective_time: "2026-12-05T10:00:00Z", notify_time: "2026-11-02T09:00:01Z" },
      { subscription_id: "s2", customer_external_id: "cus_b", state: "skipped", reason: "renews_soon", effective_time: null, notify_time: null },
    ];
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => {
      if (c.path === "/v1/plans") return { body: { plans: [plan] } };
      if (c.path === "/v1/plans/pro/versions/latest") return { body: version };
      if (c.method === "GET" && c.path === "/v1/price-changes") return { body: { price_changes: [open, older] } };
      if (c.method === "GET" && c.path === "/v1/price-changes/pc-1") return { body: { price_change: open, items } };
      if (c.method === "POST" && c.path === "/v1/price-changes/pc-1/retry") {
        open = { ...open, scheduled: 4, skipped: 1 };
        return { body: { scheduled: 1, scheduled_subscription_ids: ["s2"], skipped: [{ subscription_id: "s9", customer_external_id: "cus_z", reason: "not_changeable" }], to_version: 2, notice_days: 30, price_change_id: "pc-1", already_scheduled: 3 } };
      }
      if (c.method === "POST" && c.path === "/v1/price-changes/pc-1/withdraw") {
        open = { ...open, state: "withdrawn", scheduled: 0, withdrawn: 4, withdraw_time: "2026-11-03T09:00:00Z", withdrawn_by: "boss@example.com" };
        return { body: { price_change: open, withdrawn: 4 } };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await rowAction(page.getByRole("row", { name: /pro/ }), "Schedule a price change");
    const sheet = page.getByTestId("price-change-sheet");
    const row = sheet.getByTestId("pc-row-pc-1");
    await expect(row).toContainText("v1 to v2");
    await expect(row).toContainText("Open");
    await expect(row).toContainText("boss@example.com");
    await expect(row).toContainText("3 waiting, 0 moved, 0 withdrawn, 2 skipped");
    await expect(sheet.getByTestId("pc-row-pc-0")).toContainText("Withdrawn");
    // A withdrawn price change offers neither action.
    await expect(sheet.getByTestId("pc-retry-pc-0")).toHaveCount(0);
    await expect(sheet.getByTestId("pc-withdraw-pc-0")).toHaveCount(0);
    // The customers of a price change, with what became of each.
    await sheet.getByTestId("pc-details-pc-1").click();
    const table = sheet.getByTestId("pc-items-pc-1");
    await expect(table).toContainText("cus_a");
    await expect(table).toContainText("Waiting");
    await expect(table).toContainText("renews too soon");
    // Retry runs the price change again, without a question: it only tells customers what the seller already decided to tell them.
    await sheet.getByTestId("pc-retry-pc-1").click();
    await expect(sheet.getByTestId("pc-notice-done")).toContainText("1 more subscriptions scheduled; 1 still left out");
    expect(gw.called("POST", "/v1/price-changes/pc-1/retry")).toHaveLength(1);
    // Withdrawing asks first, and says what stays.
    await sheet.getByTestId("pc-withdraw-pc-1").click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("Changes already applied stay");
    await confirmDialog(page);
    await expect(sheet.getByTestId("pc-notice-done")).toContainText("4 waiting changes withdrawn");
    await expect(sheet.getByTestId("pc-withdraw-pc-1")).toHaveCount(0);
    await expect(row).toContainText("Withdrawn");
    expect(gw.called("POST", "/v1/price-changes/pc-1/withdraw")).toHaveLength(1);
  });

  test("a version is published with amounts in minor units, one fee per currency", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => {
      if (c.method === "POST" && c.path === "/v1/plans") return { status: 201, body: { code: "team", name: "Team" } };
      if (c.method === "POST" && c.path === "/v1/plans/team/versions") return { status: 201, body: { version: 1 } };
      if (c.path === "/v1/plans") return { body: { plans: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("team");
    await form.getByLabel("Name", { exact: true }).fill("Team");
    await form.getByLabel("Fixed fee per month (TRY)").fill("12,50");
    await form.getByRole("button", { name: "Add a fee in another currency" }).click();
    await pickCurrency(form.getByLabel("Currency of the extra fee"), "USD");
    await form.getByLabel("Billing interval of the extra fee").selectOption("month");
    await form.getByLabel("Fixed fee 1").fill("3");
    await form.getByRole("button", { name: "Add a fee in another currency" }).click();
    await pickCurrency(form.getByLabel("Currency of the extra fee").nth(1), "TRY");
    await form.getByLabel("Fixed fee 2").fill("120");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
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
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => {
      if (c.method === "POST" && c.path === "/v1/plans") return { status: 409, body: { error: { code: "ALREADY_EXISTS", message: "plan already exists" } } };
      if (c.method === "POST" && c.path === "/v1/plans/pro/versions") return { status: 201, body: { version: 3 } };
      if (c.path === "/v1/plans") return { body: { plans: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("pro");
    await form.getByLabel("Name", { exact: true }).fill("Pro");
    await form.getByLabel("Fixed fee per month (TRY)").fill("100");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/pro/versions").length).toBe(1);
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("an amount with too many decimals is refused before anything is sent", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(tenantList).on((c) => (c.path === "/v1/plans" ? { body: { plans: [] } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/packages");
    await page.getByTestId("packages-new").click();
    const form = page.getByRole("form", { name: "new package" });
    await form.getByLabel("Code").fill("team");
    await form.getByLabel("Name", { exact: true }).fill("Team");
    await form.getByLabel("Fixed fee per month (TRY)").fill("1.005");
    await page.getByRole("dialog").getByRole("button", { name: "Publish version" }).click();
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
    owner: "payrudder",
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
      if (c.path.startsWith("/v1/plans/") && c.path.endsWith("/versions/latest")) return { body: { version: 1, base_fee_minor: "1000", publish_time: "2026-10-05T10:00:00Z" } };
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
    expect(gw.called("GET", "/v1/customers/cus_1/entitlements")[0]?.headers["x-payrudder-tenant"]).toBe(TENANT);
  });

  test("a subscription is created for a customer and a package, with an optional trial and provider", async ({ page }) => {
    const gw = common(new MockGateway(), []).on((c) => (c.method === "POST" && c.path === "/v1/subscriptions" ? { status: 201, body: sub("s9") } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByTestId("subscriptions-new").click();
    const form = page.getByRole("form", { name: "new subscription" });
    await pickPlace(form.getByLabel("Customer"), "cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await form.getByLabel("Billing interval").selectOption("SUBSCRIPTION_INTERVAL_YEAR");
    await form.getByLabel("Trial days").fill("14");
    await form.getByLabel("Provider").selectOption("iyzico");
    await page.getByRole("dialog").getByRole("button", { name: "Create subscription" }).click();
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
    await page.getByTestId("subscriptions-new").click();
    const form = page.getByRole("form", { name: "new subscription" });
    await pickPlace(form.getByLabel("Customer"), "cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await form.getByLabel("Coupon code").fill("WELCOME");
    await page.getByRole("dialog").getByRole("button", { name: "Create subscription" }).click();
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

  test("a Payrudder-run subscription can be paused, moved to another package and cancelled at the period end", async ({ page }) => {
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
    const panel = page.getByRole("region", { name: "Change plan, interval or seats" });
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

  test("seats are set when a subscription is made, shown in the list and the sheet, and refused out of range before anything is sent", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE", quantity: 5 }), sub("s0", { customer_external_id: "cus_2" })]).on((c) =>
      c.method === "POST" && c.path === "/v1/subscriptions" ? { status: 201, body: sub("s9") } : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    // The list shows the seats; a subscription made before seats existed has one.
    await expect(page.getByRole("row", { name: /cus_1/ }).getByTestId("sub-seats-cell")).toHaveText("5");
    await expect(page.getByRole("row", { name: /cus_2/ }).getByTestId("sub-seats-cell")).toHaveText("1");
    await page.getByTestId("subscriptions-new").click();
    const form = page.getByRole("form", { name: "new subscription" });
    await expect(form.getByTestId("seats-hint")).toContainText("fixed fee is multiplied by the seats");
    await pickPlace(form.getByLabel("Customer"), "cus_2");
    await form.getByLabel("Package").selectOption("pro");
    const create = page.getByRole("dialog").getByRole("button", { name: "Create subscription" });
    await form.getByLabel("Seats").fill("0");
    await expect(form.getByRole("alert")).toContainText("whole number from 1 to 100000");
    await create.click();
    expect(gw.called("POST", "/v1/subscriptions")).toHaveLength(0);
    await form.getByLabel("Seats").fill("12");
    await create.click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toMatchObject({ customer_external_id: "cus_2", plan_code: "pro", quantity: 12 });
    // Left empty, the seats are not sent: the server's default is one.
    await page.getByTestId("subscriptions-new").click();
    await pickPlace(form.getByLabel("Customer"), "cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await create.click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(2);
    expect(gw.called("POST", "/v1/subscriptions")[1]?.body).not.toHaveProperty("quantity");
    // The sheet of the five-seat subscription says so.
    await page.getByRole("row", { name: /cus_1/ }).getByRole("button", { name: "Manage" }).click();
    await expect(page.getByTestId("sub-seats")).toContainText("5");
  });

  test("seats are changed with a preview first: only a different number is a change, and the quote names the seats after it", async ({ page }) => {
    const quote = {
      timing: "CHANGE_TIMING_IMMEDIATELY",
      proration: "CHANGE_PRORATION_PRORATE",
      effective_time: "2026-10-16T00:00:00Z",
      credit_minor: "500",
      charge_minor: "1500",
      net_minor: "1000",
      currency: "TRY",
      next_renewal_time: "2026-11-01T00:00:00Z",
      next_renewal_minor: "3000",
      quantity: 8,
    };
    const gw = common(new MockGateway(), [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE", quantity: 5, scheduled_change: { plan_code: "basic", plan_version: 1, interval: "SUBSCRIPTION_INTERVAL_MONTH", quantity: 3, effective_time: "2026-11-01T00:00:00Z" } })]).on((c) => {
      if (c.method === "POST" && c.path === "/v1/subscriptions/s1/change-preview") return { body: quote };
      if (c.method === "POST" && c.path === "/v1/subscriptions/s1/change") return { body: { subscription: sub("s1", { quantity: 8 }), quote } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Change plan, interval or seats" });
    await expect(panel).toContainText("Scheduled: basic (Monthly), 3 seats from");
    const preview = panel.getByRole("button", { name: "Preview" });
    await expect(preview).toBeDisabled();
    // The number the subscription has already is no change; neither is a bad one.
    await panel.getByTestId("change-seats").fill("5");
    await expect(preview).toBeDisabled();
    await panel.getByTestId("change-seats").fill("0");
    await expect(panel.getByRole("alert")).toContainText("whole number from 1 to 100000");
    await expect(preview).toBeDisabled();
    await panel.getByTestId("change-seats").fill("8");
    await preview.click();
    const result = panel.getByLabel("What this change costs");
    await expect(result.getByTestId("quote-seats")).toHaveText("8");
    await expect(result).toContainText("10.00 is invoiced now");
    expect(gw.called("POST", "/v1/subscriptions/s1/change-preview")[0]?.body).toEqual({ quantity: 8 });
    await panel.getByRole("button", { name: "Apply change" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/change").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions/s1/change")[0]?.body).toEqual({ quantity: 8 });
    // The panel clears itself once the change has gone through (the quote disappears): only then is it ready for the next one.
    await expect(result).toHaveCount(0);
    // The seats can be changed along with the package; both are sent.
    await panel.getByTestId("change-seats").fill("9");
    await panel.getByLabel("New package").selectOption("pro");
    await preview.click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions/s1/change-preview").length).toBe(2);
    expect(gw.called("POST", "/v1/subscriptions/s1/change-preview")[1]?.body).toEqual({ plan_code: "pro", quantity: 9 });
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
    await expect(panel).toContainText("Paddle runs this subscription");
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
    expect(call?.headers["x-payrudder-tenant"]).toBeUndefined();
  });

  test("ending a prepaid subscription can credit the unused part", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1", { fee_timing: "FEE_TIMING_ADVANCE" }), sub("s2", { fee_timing: "FEE_TIMING_ADVANCE", coupon_code: "TEN" })]).on((c) =>
      c.method === "DELETE" && c.path === "/v1/subscriptions/s1" ? { body: { subscription: sub("s1"), credited_minor: "1999" } } : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).first().click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await panel.getByRole("button", { name: "End now and credit the unused part" }).click();
    await confirmDialog(page, "yes");
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1").length).toBe(1);
    expect(gw.called("DELETE", "/v1/subscriptions/s1")[0]?.query.get("credit_unused")).toBe("true");
    await expect(panel.getByRole("status")).toContainText("19.99");
    // A subscription with a coupon is left to a credit note.
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Manage" }).nth(1).click();
    await expect(page.getByRole("button", { name: "End now and credit the unused part" })).toHaveCount(0);
  });

  test("ending a subscription asks first", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s1")]).on((c) => (c.method === "DELETE" && c.path === "/v1/subscriptions/s1" ? { body: sub("s1") } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await panel.getByRole("button", { name: "End now" }).click();
    await confirmDialog(page, "no");
    expect(gw.called("DELETE", "/v1/subscriptions/s1")).toHaveLength(0);
    await panel.getByRole("button", { name: "End now" }).click();
    await confirmDialog(page, "yes");
    await expect.poll(() => gw.called("DELETE", "/v1/subscriptions/s1").length).toBe(1);
  });
});

test.describe("pricing", () => {
  const quiet = (c: { path: string; method: string }) => {
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
    await openTenantPage(page, "/invoices?invoice=inv1");
    await expect(page.getByRole("row", { name: /Base fee/ })).toContainText("Oct 1, 2026");
    await page.getByRole("button", { name: "New credit note" }).click();
    const form = page.getByRole("form", { name: "new credit note" });
    await form.getByLabel("Amount").fill("25.50");
    await form.getByLabel("Reason").fill("goodwill");
    await page.getByRole("button", { name: "Issue credit note" }).click();
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
    await openTenantPage(page, "/invoices?invoice=inv1");
    await page.getByRole("button", { name: "New credit note" }).click();
    const form = page.getByRole("form", { name: "new credit note" });
    await form.getByLabel("Amount").fill("10");
    await form.getByLabel("Refund to the card").check();
    await page.getByRole("button", { name: "Issue credit note" }).click();
    await expect.poll(() => gw.called("POST", "/admin/v1/invoices/inv1/credit-notes").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/invoices/inv1/credit-notes")[0]?.body).toEqual({ tenant_id: TENANT, amount_minor: "1000", reason: "", refund_to_card: true });
    const row = page.getByRole("row", { name: /CN-2026-000001/ });
    await expect(row).toContainText("Refund failed");
    await expect(row).toContainText("card closed");
    await rowAction(row, "Retry refund");
    await expect.poll(() => gw.called("POST", "/admin/v1/credit-notes/cn1/retry-refund").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/retry-refund")[0]?.body).toEqual({ tenant_id: TENANT });
    await expect(row).toContainText("Refund requested");
  });

  // A refund whose outcome is unknown is never asked of the provider again under the same key: the operator checks there and says what they found.
  const unknownRefund = {
    id: "cn1", number: "CN-2026-000001", invoice_number: "INV-2026-000001", customer_external_id: "cus_1", currency: "USD", total_minor: "1000",
    disposition: "CREDIT_NOTE_DISPOSITION_REFUND", refund_state: "REFUND_STATE_FAILED", refund_error: "REFUND_OUTCOME_UNKNOWN: iyzico was asked for this refund before", create_time: "2026-10-20T10:00:00Z",
  };

  test("a refund whose outcome is unknown is recorded as made, with the provider's reference", async ({ page }) => {
    const notes: Record<string, unknown>[] = [{ ...unknownRefund }];
    const gw = common("paid", notes).on((c) => {
      if (c.method === "POST" && c.path === "/admin/v1/credit-notes/cn1/resolve-refund") {
        notes[0] = { ...notes[0], refund_state: "REFUND_STATE_REFUNDED", refund_error: "" };
        return { body: notes[0] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices?invoice=inv1");
    const row = page.getByRole("row", { name: /CN-2026-000001/ });
    await expect(row).toContainText("REFUND_OUTCOME_UNKNOWN");
    await rowAction(row, "Resolve refund");
    const form = page.getByRole("form", { name: "resolve refund" });
    await expect(form).toContainText("iyzico was asked for this refund before");
    // The reference is required when the refund was made: nothing is sent without it.
    await page.getByTestId("resolve-submit").click();
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund")).toHaveLength(0);
    await form.getByLabel("Refund reference").fill("  ref_8841 ");
    await form.getByLabel("What you checked").fill("seen in the iyzico dashboard");
    await page.getByTestId("resolve-submit").click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("ref_8841");
    await confirmDialog(page);
    await expect.poll(() => gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund")[0]?.body).toEqual({
      tenant_id: TENANT, resolution: "REFUND_RESOLUTION_REFUNDED", provider_refund_id: "ref_8841", note: "seen in the iyzico dashboard",
    });
    await expect(row).toContainText("Refunded to the card");
  });

  test("a refund that was not made is asked again only after the operator confirms", async ({ page }) => {
    const notes: Record<string, unknown>[] = [{ ...unknownRefund }];
    const gw = common("paid", notes).on((c) => {
      if (c.method === "POST" && c.path === "/admin/v1/credit-notes/cn1/resolve-refund") {
        notes[0] = { ...notes[0], refund_state: "REFUND_STATE_REQUESTED", refund_error: "" };
        return { body: notes[0] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices?invoice=inv1");
    const row = page.getByRole("row", { name: /CN-2026-000001/ });
    await rowAction(row, "Resolve refund");
    const form = page.getByRole("form", { name: "resolve refund" });
    await form.getByText("Not paid back").click();
    await expect(form.getByLabel("Refund reference")).toHaveCount(0);
    await page.getByTestId("resolve-submit").click();
    // Saying no to the question sends nothing; the refund could be paid twice if the first one went through.
    await expect(page.getByTestId("confirm-dialog")).toContainText("the customer is paid twice");
    await confirmDialog(page, "no");
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund")).toHaveLength(0);
    await page.getByTestId("resolve-submit").click();
    await confirmDialog(page);
    await expect.poll(() => gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund").length).toBe(1);
    expect(gw.called("POST", "/admin/v1/credit-notes/cn1/resolve-refund")[0]?.body).toEqual({ tenant_id: TENANT, resolution: "REFUND_RESOLUTION_NOT_REFUNDED" });
    await expect(row).toContainText("Refund requested");
  });

  test("a link to the invoice is made for the customer and can be revoked", async ({ page }) => {
    const gw = common("paid", []).on((c) => {
      if (c.method === "POST" && c.path === "/v1/invoices/inv1/links") return { status: 201, body: { token: "tok", url: "https://api.example.com/i/tok", expires_at: "2027-01-20T00:00:00Z" } };
      if (c.method === "DELETE" && c.path === "/v1/invoices/inv1/links") return { body: { revoked: 1 } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices?invoice=inv1");
    const card = page.getByRole("region", { name: "Share with the customer" });
    await card.getByLabel("Valid for (days)").fill("30");
    await card.getByRole("button", { name: "Make a link" }).click();
    await expect.poll(() => gw.called("POST", "/v1/invoices/inv1/links").length).toBe(1);
    expect(gw.called("POST", "/v1/invoices/inv1/links")[0]?.body).toEqual({ expires_in_days: 30 });
    await expect(card.getByLabel("Invoice link")).toHaveValue("https://api.example.com/i/tok");
    await card.getByLabel("Valid for (days)").fill("400");
    await card.getByRole("button", { name: "Make a link" }).click();
    await expect(card.getByRole("alert")).toContainText("1 to 365");
    expect(gw.called("POST", "/v1/invoices/inv1/links")).toHaveLength(1);
    await card.getByRole("button", { name: "Revoke all links" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/invoices/inv1/links").length).toBe(1);
    await expect(card.getByRole("status")).toContainText("revoked");
  });

  test("an invoice that is not paid cannot be credited here, and an empty amount credits what is left", async ({ page }) => {
    const gw = common("open", []);
    await gw.install(page);
    await openTenantPage(page, "/invoices?invoice=inv1");
    await expect(page.getByRole("button", { name: "New credit note" })).toHaveCount(0);
    await expect(page.getByText("No credit notes.")).toBeVisible();

    const paid = common("paid", []).on((c) => (c.method === "POST" && c.path === "/admin/v1/invoices/inv1/credit-notes" ? { status: 201, body: { id: "cn" } } : undefined));
    const page2 = await page.context().newPage();
    await paid.install(page2);
    await openTenantPage(page2, "/invoices?invoice=inv1");
    await page2.getByRole("button", { name: "New credit note" }).click();
    await page2.getByRole("button", { name: "Issue credit note" }).click();
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
        if (c.path.startsWith("/v1/plans/") && c.path.endsWith("/versions/latest")) return { body: { version: 1, base_fee_minor: "1000", publish_time: "2026-10-05T10:00:00Z" } };
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

  test("a cancellation the provider has announced shows beside the status and in the sheet, read-only", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => {
        if (c.path === `/admin/v1/tenants/${TENANT}/provider-subscriptions`) {
          return {
            body: {
              subscriptions: [
                { provider: "paddle", environment: "live", provider_ref: "sub_end", status: "active", updated_at: "2026-10-20T10:00:00Z", scheduled_change: { action: "cancel", effective_at: "2026-12-01T00:00:00Z" } },
                { provider: "paddle", environment: "live", provider_ref: "sub_plain", status: "active", updated_at: "2026-10-19T10:00:00Z" },
              ],
            },
          };
        }
        return undefined;
      });
    await gw.install(page);
    await openTenantPage(page, "/providers");
    await expect(page.getByRole("row", { name: /sub_end/ })).toContainText("Cancels on");
    await expect(page.getByRole("row", { name: /sub_plain/ })).not.toContainText("Cancels on");
    await page.getByRole("row", { name: /sub_end/ }).click();
    await expect(page.getByRole("dialog")).toContainText("Announced by the provider");
    await expect(page.getByRole("dialog")).toContainText("Cancels on");
    await page.keyboard.press("Escape");
    await page.getByRole("row", { name: /sub_plain/ }).click();
    await expect(page.getByRole("dialog")).toContainText("Nothing announced");
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
    await page.getByRole("button", { name: "Add or replace an account" }).click();
    const form = page.getByRole("dialog");
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
    // The sheet closes once the account is saved, and opens clean: the keys are not shown again.
    await page.getByRole("button", { name: "Add or replace an account" }).click();
    await form.getByLabel("Provider").selectOption("paddle");
    await expect(form.getByLabel("Settings (JSON)")).not.toHaveValue(/"k"/);
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
    await pickCurrency(page.getByLabel("Currency of rule 2"), "USD");
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
    await pickCurrency(preview.getByLabel("Currency"), "USD");
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
  const seen = { first_seen_at: "2026-10-20T10:00:00Z", last_seen_at: "2026-10-21T10:00:00Z", environment: "live", period: "2026-10" };
  const queue = (extra: Record<string, unknown>[] = []) => ({
    discrepancies: [
      { id: 1, kind: "paid_without_charge", invoice_id: "inv-1", number: "INV-2026-000001", currency: "TRY", billed_minor: 500, status: "open", ...seen },
      { id: 2, kind: "amount_mismatch", invoice_id: "inv-2", number: "INV-2026-000002", provider: "iyzico", provider_ref: "P2", currency: "TRY", billed_minor: 700, ledger_minor: 650, status: "explained", owner: "ayse", note: "fee", ...seen },
      ...extra,
    ],
    counts: [
      { status: "open", kind: "paid_without_charge", n: 1 },
      { status: "explained", kind: "amount_mismatch", n: 1 },
    ],
    next_page_token: "",
    last_run: { environment: "live", period: "2026-10", started_by: "schedule", actor: "system", result: "ok", found: 2, opened: 0, reopened: 0, cleared: 0, finished_at: "2026-10-21T10:00:00Z" },
  });

  test("the ledger is listed and the review queue keeps what a run found", async ({ page }) => {
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
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`) return { body: queue() };
      if (c.method === "POST" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/runs`) return { body: { period: "2026-10", paid_invoices: 3, successful_charges: 2, found: 2, opened: 1, reopened: 0, cleared: 0, discrepancy_counts: {} } };
      if (c.path === `/admin/v1/tenants/${TENANT}/settlement/payments`) return { body: { period: c.query.get("period"), payments: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    const entry = page.getByRole("row", { name: /iyzico/ }).filter({ hasText: "P1" });
    await expect(entry).toContainText("11.16");
    await expect(entry).toContainText("succeeded");

    // The queue loads what needs a person (open and explained), says when the schedule last ran, and counts by status.
    expect(gw.called("GET", `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`)[0]?.query.get("status")).toBe("open,explained");
    await expect(page.getByTestId("last-run")).toContainText("schedule");
    await expect(page.getByLabel("Discrepancies by status")).toContainText("Open: 1");
    const mismatch = page.getByRole("row", { name: /INV-2026-000002/ });
    await expect(mismatch).toContainText("6.50");
    await expect(mismatch).toContainText("Explained");
    await expect(mismatch).toContainText("ayse");

    const form = page.getByRole("form", { name: "Reconcile with the ledger" });
    await form.getByLabel("Period (YYYY-MM)").fill("2026-10");
    await form.getByRole("button", { name: "Reconcile" }).click();
    await expect(page.getByTestId("run-result")).toContainText("2 found, 1 new");
    const run = gw.called("POST", `/admin/v1/tenants/${TENANT}/reconciliation/runs`)[0];
    expect(run?.query.get("environment")).toBe("live");
    expect(run?.body).toEqual({ period: "2026-10" });
  });

  test("a discrepancy is explained with a note, and many are resolved together after a question", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) return { body: { entries: [] } };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`) return { body: queue() };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies/1`) return { body: { discrepancy: queue().discrepancies[0], events: [{ id: 1, action: "detected", actor: "system", to_status: "open", created_at: "2026-10-20T10:00:00Z" }] } };
      if (c.method === "POST" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies/2/resolve`) return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "payments.reconcile.bad_resolution", message: "no" } } };
      if (c.method === "POST" && /\/reconciliation\/discrepancies\/\d+\/(explain|resolve)$/.test(c.path)) return { body: { discrepancy: queue().discrepancies[0] } };
      if (c.path === `/admin/v1/tenants/${TENANT}/settlement/payments`) return { body: { period: "2026-10", payments: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");

    // A row's own action opens the form: a note is required before the button works.
    await page.getByRole("row", { name: /INV-2026-000001/ }).getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Explain" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("button", { name: "Explain" })).toBeDisabled();
    await dialog.getByLabel("Note").fill("waiting for the provider's report");
    await dialog.getByRole("button", { name: "Explain" }).click();
    expect(gw.called("POST", `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies/1/explain`)[0]?.body).toEqual({ note: "waiting for the provider's report" });

    // Both rows ticked: resolve them with one resolution; it asks first, and the one the server refuses is named and stays ticked.
    await page.getByRole("checkbox", { name: "Select all rows on this page" }).click();
    await page.getByRole("button", { name: "Resolve selected" }).click();
    const form = page.getByRole("dialog");
    await expect(form.getByRole("combobox", { name: "Resolution" }).locator("option")).not.toContainText(["Ledger fact written"]);
    await form.getByLabel("Note").fill("checked at the provider");
    await form.getByRole("button", { name: "Resolve" }).click();
    await expect(page.getByTestId("confirm-dialog")).toContainText("Resolve 2 discrepancies?");
    await page.getByTestId("confirm-accept").click();
    await expect(page.getByTestId("bulk-note")).toContainText("1 done, 1 not changed");
    const bodies = gw.called("POST", /\/reconciliation\/discrepancies\/\d+\/resolve$/);
    expect(bodies.map((b) => b.path.split("/").at(-2))).toEqual(["1", "2"]);
    expect(bodies[0]?.body).toEqual({ resolution: "credit_note", note: "checked at the provider" });
  });

  test("a discrepancy opens with its history, and a clean queue says so", async ({ page }) => {
    let empty = false;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) return { body: { entries: [] } };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`) return { body: empty ? { discrepancies: [], counts: [], next_page_token: "" } : queue() };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies/1`) {
        return { body: { discrepancy: queue().discrepancies[0], events: [{ id: 1, action: "detected", actor: "system", to_status: "open", created_at: "2026-10-20T10:00:00Z" }] } };
      }
      if (c.path === `/admin/v1/tenants/${TENANT}/settlement/payments`) return { body: { period: "2026-10", payments: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    await page.getByRole("row", { name: /INV-2026-000001/ }).click();
    await expect(page.getByTestId("discrepancy-events")).toContainText("Found");
    await expect(page.getByTestId("discrepancy-events")).toContainText("system");
    await page.keyboard.press("Escape");
    empty = true;
    await page.reload();
    await expect(page.getByText("Nothing to review.")).toBeVisible();
    await expect(page.getByText("Nothing in the ledger yet.")).toBeVisible();
  });

  test("a period's payments are paid, reported or settled, and a report is imported as a file", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `/admin/v1/tenants/${TENANT}/ledger`) return { body: { entries: [] } };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`) return { body: queue() };
      if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/settlement/payments`) {
        return {
          body: {
            period: c.query.get("period"),
            payments: [
              { invoice_id: "inv-a", number: "INV-A", provider: "stripe", provider_ref: "pi_a", currency: "USD", amount_minor: 1000, paid_at: "2026-10-03T09:00:00Z", state: "settled", settlement_currency: "USD", fee_minor: 59, expected_fee_minor: 59, settled_at: "2026-10-12T00:00:00Z", settlement_batch: "po_1" },
              { invoice_id: "inv-b", number: "INV-B", provider: "stripe", provider_ref: "pi_b", currency: "USD", amount_minor: 500, paid_at: "2026-10-04T09:00:00Z", state: "pending_payout" },
              { invoice_id: "inv-c", number: "INV-C", provider: "stripe", provider_ref: "pi_c", currency: "USD", amount_minor: 700, paid_at: "2026-10-05T09:00:00Z", state: "not_reported" },
            ],
          },
        };
      }
      if (c.method === "POST" && c.path === `/admin/v1/tenants/${TENANT}/settlement/import`) return { body: { lines: 3, changed: 2 } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    await expect(page.getByLabel("Payments by settlement state")).toContainText("Settled: 1");
    const settled = page.getByRole("row", { name: /INV-A/ });
    await expect(settled).toContainText("Settled");
    await expect(settled).toContainText("po_1");
    await expect(settled).toContainText("expected");
    await expect(page.getByRole("row", { name: /INV-B/ })).toContainText("Reported, awaiting payout");
    await expect(page.getByRole("row", { name: /INV-C/ })).toContainText("Paid, not reported");

    await page.getByLabel("Report (CSV)").setInputFiles({ name: "stripe.csv", mimeType: "text/csv", buffer: Buffer.from("reference,payment_ref,currency,gross,occurred_on\nl1,pi_a,USD,10.00,2026-10-03\n") });
    await page.getByLabel("Provider (account key)").fill("stripe");
    await page.getByRole("button", { name: "Import report" }).click();
    await expect(page.getByTestId("import-result")).toContainText("3 line(s) read, 2 new or changed");
    const imp = gw.called("POST", `/admin/v1/tenants/${TENANT}/settlement/import`)[0];
    expect(imp?.query.get("environment")).toBe("live");
    expect(imp?.query.get("provider")).toBe("stripe");
  });
});

test.describe("fee rules", () => {
  const rule = (provider: string, currency: string, bps: number, fixed: number) => ({ environment: "live", provider, currency, rate_bps: bps, fixed_minor: fixed, updated_at: "2026-10-20T10:00:00Z" });

  test("the expected fees are listed, filtered, added, edited and removed; a rate is typed as a percentage and sent in hundredths", async ({ page }) => {
    const rules = [rule("stripe", "USD", 290, 30), rule("paddle~eu", "EUR", 500, 50)];
    const base = `/admin/v1/tenants/${TENANT}`;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `${base}/ledger`) return { body: { entries: [] } };
      if (c.method === "GET" && c.path === `${base}/reconciliation/discrepancies`) return { body: { discrepancies: [], counts: [], next_page_token: "" } };
      if (c.path === `${base}/settlement/payments`) return { body: { period: c.query.get("period"), payments: [] } };
      if (c.path !== `${base}/settlement/fee-rules`) return undefined;
      if (c.method === "GET") return { body: { rules } };
      if (c.method === "PUT") {
        const next = { environment: "live", ...c.body, updated_at: "2026-11-02T10:00:00Z" } as ReturnType<typeof rule>;
        const i = rules.findIndex((r) => r.provider === next.provider && r.currency === next.currency);
        if (i >= 0) rules[i] = next;
        else rules.push(next);
        return { body: { rule: next } };
      }
      if (c.method === "DELETE") {
        const i = rules.findIndex((r) => r.provider === c.query.get("provider") && r.currency === c.query.get("currency"));
        if (i < 0) return { status: 404, body: { error: { code: "NOT_FOUND", key: "payments.settlement.fee_rule_not_found", message: "no" } } };
        rules.splice(i, 1);
        return { status: 204, body: {} };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    const card = page.getByTestId("fee-rules-card");
    await expect(card.getByTestId("fee-rule-stripe-USD")).toContainText("2.9%");
    await expect(card.getByTestId("fee-rule-stripe-USD")).toContainText("$0.30");
    await expect(card.getByTestId("fee-rule-paddle~eu-EUR")).toContainText("5%");

    // A filter applies on its button, like every list.
    await pickFilter(page, "Currency", "EUR");
    await expect(card.getByTestId("fee-rule-stripe-USD")).toHaveCount(0);
    await expect(card.getByTestId("fee-rule-paddle~eu-EUR")).toBeVisible();
    await card.getByRole("button", { name: "Clear" }).first().click();
    await expect(card.getByTestId("fee-rule-stripe-USD")).toBeVisible();

    // A row opens its detail beside the list; editing keeps the rule's name and sends the new numbers.
    await card.getByTestId("fee-rule-stripe-USD").click();
    const detail = page.getByTestId("detail-sheet");
    await expect(detail).toContainText("Stripe");
    await expect(detail).toContainText("2.9%");
    await detail.getByTestId("fee-rule-edit").click();
    const sheet = page.getByTestId("fee-rule-sheet");
    await expect(sheet.getByTestId("fee-rule-provider")).toBeDisabled();
    await expect(sheet.getByTestId("fee-rule-rate")).toHaveValue("2.9");
    await expect(sheet.getByTestId("fee-rule-fixed")).toHaveValue("0.30");
    await sheet.getByTestId("fee-rule-rate").fill("3.1");
    await sheet.getByTestId("fee-rule-fixed").fill("0.35");
    await sheet.getByTestId("fee-rule-save").click();
    await expect(sheet).toBeHidden();
    expect(gw.called("PUT", `${base}/settlement/fee-rules`)[0]?.body).toEqual({ environment: "live", provider: "stripe", currency: "USD", rate_bps: 310, fixed_minor: 35 });
    await page.keyboard.press("Escape");
    await expect(card.getByTestId("fee-rule-stripe-USD")).toContainText("3.1%");
    await expect(card.getByTestId("fee-rule-stripe-USD")).toContainText("$0.35");

    // A new rule: a wrong rate is said before anything is sent; a right one is saved with the currency in capitals.
    await card.getByTestId("fee-rule-add").click();
    await sheet.getByTestId("fee-rule-provider").fill("paddle");
    await sheet.getByTestId("fee-rule-currency").fill("usd");
    await sheet.getByTestId("fee-rule-rate").fill("101");
    await sheet.getByTestId("fee-rule-save").click();
    await expect(sheet.getByTestId("fee-rule-problem")).toContainText("0 to 100");
    expect(gw.called("PUT", `${base}/settlement/fee-rules`)).toHaveLength(1);
    await sheet.getByTestId("fee-rule-rate").fill("5");
    await sheet.getByTestId("fee-rule-save").click();
    await expect(card.getByTestId("fee-rule-paddle-USD")).toContainText("5%");
    expect(gw.called("PUT", `${base}/settlement/fee-rules`)[1]?.body).toEqual({ environment: "live", provider: "paddle", currency: "USD", rate_bps: 500, fixed_minor: 0 });

    // Removing asks first and names the rule.
    await rowAction(card.getByTestId("fee-rule-paddle~eu-EUR"), "Remove");
    await expect(page.getByTestId("confirm-dialog")).toContainText("paddle~eu");
    await confirmDialog(page, "yes");
    await expect(card.getByTestId("fee-rule-paddle~eu-EUR")).toHaveCount(0);
    const del = gw.called("DELETE", `${base}/settlement/fee-rules`)[0];
    expect([del?.query.get("environment"), del?.query.get("provider"), del?.query.get("currency")]).toEqual(["live", "paddle~eu", "EUR"]);
  });

  test("with no rule the card says what that means", async ({ page }) => {
    const base = `/admin/v1/tenants/${TENANT}`;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === `${base}/ledger`) return { body: { entries: [] } };
      if (c.method === "GET" && c.path === `${base}/reconciliation/discrepancies`) return { body: { discrepancies: [], counts: [], next_page_token: "" } };
      if (c.path === `${base}/settlement/payments`) return { body: { period: c.query.get("period"), payments: [] } };
      if (c.path === `${base}/settlement/fee-rules`) return { body: { rules: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payments");
    await expect(page.getByTestId("fee-rules-card")).toContainText("No fee rules");
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
    // The invoice opens over this page, not on the invoices list.
    await page.getByRole("button", { name: "INV-2026-000009" }).click();
    await expect(page).toHaveURL(/sheet=peek(%3A|:)invoice/);
    await expect(page.getByText("given up").first()).toBeVisible();
  });
});

test("the billing pages are translated", async ({ page }) => {
  const gw = baseTenant(new MockGateway()).on((c) => (c.path === "/v1/customers" ? { body: { customers: [] } } : undefined));
  await gw.install(page);
  await page.goto("/signin");
  await pickLanguage(page, "tr");
  await page.getByLabel("E-posta").fill(OPERATOR_EMAIL);
  await page.getByLabel("Parola", { exact: true }).fill(OPERATOR_PASSWORD);
  await page.getByRole("button", { name: "Giriş yap" }).click();
  await page.waitForURL("**/app");
  await page.goto("/operator");
  await page.waitForURL("**/tenants");
  await page.goto(`/tenants/${TENANT}/customers`);
  await expect(page.getByRole("link", { name: "Müşteriler" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Abonelikler" })).toBeVisible();
  await page.getByTestId("customers-new").click();
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
    await page.getByTestId("webhooks-new").click();
    const form = page.getByRole("form", { name: "new webhook endpoint" });
    await form.getByLabel("Endpoint URL").fill("https://example.com/hook");
    await form.getByLabel("invoice.paid").check();
    await page.getByRole("dialog").getByRole("button", { name: "Add endpoint" }).click();
    await expect.poll(() => gw.called("POST", "/v1/webhook-endpoints").length).toBe(1);
    expect(gw.called("POST", "/v1/webhook-endpoints")[0]?.body).toEqual({ url: "https://example.com/hook", description: "", event_types: ["invoice.paid"] });
    await expect(page.getByTestId("webhook-secret")).toHaveText("whsec_ABC123");
    const row = page.getByRole("row", { name: /example.com\/hook/ });
    await expect(row).toContainText("invoice.paid");
    await row.getByTestId("row-actions").click();
    await page.getByTestId("row-action-deliveries").click();
    const log = page.getByRole("region", { name: "Deliveries" });
    await expect(log).toContainText("Failed");
    await expect(log).toContainText("500");
    await expect(log).toContainText("nope");
    await log.getByRole("button", { name: "Send again" }).click();
    await expect.poll(() => gw.called("POST", "/v1/webhook-deliveries/d1/redeliver").length).toBe(1);
  });
});
