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
    await form.getByLabel("Fixed fee 1").fill("3");
    await form.getByRole("button", { name: "Publish version" }).click();
    await expect.poll(() => gw.called("POST", "/v1/plans/team/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/plans")[0]?.body).toEqual({ code: "team", name: "Team" });
    expect(gw.called("POST", "/v1/plans/team/versions")[0]?.body).toEqual({ base_fee_minor: "1250", fees: [{ currency: "USD", base_fee_minor: "300" }] });
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

  test("a subscription is created for a customer and a package, with an optional trial and provider", async ({ page }) => {
    const gw = common(new MockGateway(), []).on((c) => (c.method === "POST" && c.path === "/v1/subscriptions" ? { status: 201, body: sub("s9") } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    const form = page.getByRole("form", { name: "new subscription" });
    await form.getByLabel("Customer").selectOption("cus_2");
    await form.getByLabel("Package").selectOption("pro");
    await form.getByLabel("Trial days").fill("14");
    await form.getByLabel("Provider").selectOption("iyzico");
    await form.getByRole("button", { name: "Create subscription" }).click();
    await expect.poll(() => gw.called("POST", "/v1/subscriptions").length).toBe(1);
    expect(gw.called("POST", "/v1/subscriptions")[0]?.body).toEqual({ customer_external_id: "cus_2", plan_code: "pro", trial_days: 14, provider: "iyzico" });
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

  test("a subscription a provider runs cannot be changed here, only moved", async ({ page }) => {
    const gw = common(new MockGateway(), [sub("s2", { provider: "paddle", provider_ref: "sub_01", owner: "provider", currency: "USD" })]);
    await gw.install(page);
    await openTenantPage(page, "/subscriptions");
    await expect(page.getByRole("row", { name: /cus_1/ })).toContainText("run by provider");
    await page.getByRole("button", { name: "Manage" }).click();
    const panel = page.getByRole("region", { name: "Manage cus_1" });
    await expect(panel).toContainText("paddle runs this subscription");
    await expect(panel.getByRole("button", { name: "Pause" })).toHaveCount(0);
    await expect(panel.getByRole("button", { name: "Change package" })).toHaveCount(0);
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

test.describe("providers", () => {
  const quiet = (c: { path: string; method: string }) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-accounts` && c.method === "GET") return { body: { accounts: [] } };
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-subscriptions`) return { body: { subscriptions: [] } };
    if (c.path === "/v1/routing-rules" && c.method === "GET") return { body: { rules: [] } };
    return undefined;
  };

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
