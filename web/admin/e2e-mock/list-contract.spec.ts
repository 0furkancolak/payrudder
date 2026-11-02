import { expect, test, type Locator, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, signInAsOperator, TENANT } from "./gateway";
import { APP, liveGateway, LIVE, productGateway, signInAsAda } from "./product";
import { contractsGateway, withdrawal } from "./contracts";
import { METERS, usageReads } from "./usage";

// The list contract of docs/13-admin-ui.md ("Lists"), once per list instead of once per page: a filter is a draft until Apply, the list follows it after Apply, the
// cross on the chip takes it off again; one select orders the rows and, for a list with state in the address, the choice is in the address and survives a reload.
// A list is one entry of CASES: where it is, what the mocked API holds, one text per row (in the order the rows arrive), and the one sort and one filter to try.

test.use({ viewport: { width: 1440, height: 900 }, actionTimeout: 10_000 });

interface ListCase {
  /** The list, as the plan's inventory names it. */
  name: string;
  /** Opens the page (the default is a page of the operator's tenant at `path`). */
  path?: string;
  open?: (page: Page) => Promise<void>;
  mock: (gw: MockGateway) => MockGateway;
  /** What the tenant of the operator's page has besides the defaults (an app list needs the tenant's account). */
  tenant?: Record<string, unknown>;
  /** A page of the person's console (signed in as Ada) instead of the operator's. */
  person?: boolean;
  /** The part of the page the list is in, when the page has several lists. */
  scope?: (page: Page) => Locator;
  /** What a row is: a table row (the default) or a list item. */
  item?: "row" | "listitem";
  /** One text per row, in the order the rows arrive; every row of the list shows its text. */
  rows: string[];
  /** The select's choice and the order the rows then have. */
  sort?: { value: string; order: string[] };
  /** The chip (its name), the option to apply (its text) and the rows that stay. */
  filter?: { chip: string; option: string | RegExp; keep: string[] };
  /** The prefix of the list's parameters in the address (`""` for the bare ones), when the list keeps its state there. */
  url?: string;
  /** False for a list that keeps its sort in the address but not its filters (the operator's support inbox). */
  filterInUrl?: boolean;
}

const get = (path: string, body: unknown, status = 200) => (gw: MockGateway) => gw.on((c) => (c.method === "GET" && c.path === path ? { status, body } : undefined));
const all = (...mocks: ((gw: MockGateway) => MockGateway)[]) => (gw: MockGateway) => mocks.reduce((g, m) => m(g), gw);
const day = (n: number) => `2026-10-${String(n).padStart(2, "0")}T09:00:00Z`;

const sub = (id: string, customer: string, extra: Record<string, unknown> = {}) => ({ id, customer_external_id: customer, plan_code: "basic", plan_version: 1, state: "SUBSCRIPTION_STATE_ACTIVE", currency: "TRY", owner: "payrudder", interval: "SUBSCRIPTION_INTERVAL_MONTH", start_time: day(10), ...extra });
const invoice = (id: string, number: string, status: string, total: string, created: number) => ({ id, number, customer_external_id: "cus_1", status, kind: "REGULAR", currency: "TRY", total_minor: total, create_time: day(created) });

const expense = (id: string, vendor: string, category: string, amount: string, incurred: string) => ({ id, vendor, category, amount_minor: amount, currency: "TRY", incurred_on: incurred, note: "", recurring: "none", create_time: `${incurred}T09:00:00Z` });
const tenantRow = (id: string, name: string, environment: string, tier: string, currency: string, created: number) => ({ id, name, environment, tier, currency, timezone: "Europe/Istanbul", status: "TENANT_STATUS_ACTIVE", create_time: day(created) });
const coupon = (code: string, active: boolean, redeemed: number) => ({ id: code, code, name: code, percent_bps: 1000, duration: "COUPON_DURATION_ONCE", applies_to: ["COUPON_TARGET_SUBSCRIPTION"], active, redeemed, promotion_code_count: 1, create_time: day(1) });
const pack = (id: string, name: string, active: boolean, created: number) => ({ id, name, credits: "100", price_minor: "500", currency: "USD", active, create_time: day(created) });
const endpoint = (id: string, url: string, active: boolean, created: number) => ({ id, url, description: "", event_types: ["invoice.paid"], active, create_time: day(created) });

const payment = (id: string, provider: string, kind: string, status: string, amount: string, at: number) => ({ id, provider, kind, status, amount_minor: amount, currency: "USD", invoice_id: `inv_${id}`, occurred_at: day(at) });
const revenueRow = (currency: string, billed: string) => ({ period: "2026-10", provider: "", country: "", currency, invoices: "1", billed_minor: billed, collected_minor: billed, refunded_minor: "0", outstanding_minor: "0", uncollectible_minor: "0" });
const subRow = (currency: string, mrr: string) => ({ provider: "", country: "", currency, active: "3", trialing: "0", paused: "0", past_due: "0", mrr_minor: mrr, new_subscriptions: "0", new_mrr_minor: "0", churned: "0", churned_mrr_minor: "0" });
const dunningRow = (number: string, currency: string, total: string) => ({ invoice_id: `id-${number}`, number, customer_external_id: "cus_1", period: "2026-10", provider: "paddle", currency, total_minor: total, state: "UNCOLLECTIBLE", dunning_attempt: 3 });
/** The three reports share one page, so one mock answers all of them. */
const reports = () =>
  all(
    get("/v1/reports/revenue", { rows: [revenueRow("USD", "5000"), revenueRow("TRY", "150000")] }),
    get("/v1/reports/subscriptions", { month: "2026-10", rows: [subRow("EUR", "1000"), subRow("TRY", "2100000"), subRow("USD", "90000")] }),
    get("/v1/reports/dunning", { summary: [], invoices: [dunningRow("INV-D1", "TRY", "100"), dunningRow("INV-D2", "TRY", "900"), dunningRow("INV-D3", "USD", "300")] }),
  );

const issue = (id: number, number: string, kind: string, status: string, billed: number) => ({ id, kind, invoice_id: `inv-${id}`, number, currency: "TRY", billed_minor: billed, status, first_seen_at: day(20), last_seen_at: day(21), environment: "live", period: "2026-10" });
const settled = (number: string, state: string, amount: number) => ({ number, invoice_id: `inv-${number}`, provider: "iyzico", provider_ref: `ref-${number}`, state, amount_minor: amount, currency: "TRY", paid_at: day(10) });
const feeRule = (provider: string, currency: string, updated: number) => ({ provider, currency, rate_bps: 290, fixed_minor: 30, updated_at: day(updated) });
const account = (provider: string, environment: string, updated: number) => ({ provider, environment, default: false, updated_at: day(updated) });
const providerSub = (ref: string, status: string, updated: number) => ({ id: ref, provider: "paddle", provider_ref: ref, environment: "live", status, updated_at: day(updated) });
/** What the payments page asks besides the list under test: nothing to show. */
const paymentsPage = () =>
  all(
    get(`/admin/v1/tenants/${TENANT}/ledger`, { entries: [] }),
    get(`/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`, { discrepancies: [], counts: [], next_page_token: "" }),
    get(`/admin/v1/tenants/${TENANT}/settlement/payments`, { period: "2026-10", payments: [] }),
    get(`/admin/v1/tenants/${TENANT}/settlement/fee-rules`, { rules: [] }),
  );
const providersPage = () => all(get(`/admin/v1/tenants/${TENANT}/provider-accounts`, { accounts: [] }), get(`/admin/v1/tenants/${TENANT}/provider-subscriptions`, { subscriptions: [] }));

const lot = (id: string, source: string, remaining: string, created: number, expires: number) => ({ id, source, remaining, credits: remaining, create_time: day(created), expire_time: day(expires) });
const entry = (id: string, kind: string, amount: string, reason: string, created: number) => ({ id, lot_id: "l1", kind, source: "manual", amount, reason, create_time: day(created) });
/** What the credits page asks besides the list under test. */
const creditsPage = () => all(get("/v1/customers", { customers: [customer("cus_1")] }), get("/v1/credit-packs", { packs: [] }), get("/v1/customers/cus_1", customer("cus_1")));
const customer = (id: string) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com`, billing_currency: "TRY", create_time: day(1) });
const ticket = (id: string, number: number, subject: string, last: number) => ({ id, number, account_id: "a", account_name: "Ada Labs", app_id: "", app_name: "", opened_by: "u1", opened_by_name: "Ada", subject, category: "question", priority: "normal", status: "open", assignee: "", create_time: day(1), update_time: day(last), last_message_time: day(last), last_author: "customer", first_response_due: day(last), first_response_time: null, sla_breached: false, solve_time: null, message_count: 1, unread: false, tags: [] });
const appRow = (id: string, name: string, slug: string, created: number, environment: string) => ({ id, account_id: "acc-1", name, slug, create_time: day(created), tenants: [{ id: `t-${id}`, environment, currency: "TRY", timezone: "Europe/Istanbul" }] });
const creditNote = (id: string, number: string, total: string, where: "toCredit" | "refunded" | "refundFailed", created: number) => ({
  id, number, invoice_number: `INV-${number}`, customer_external_id: "cus_1", currency: "TRY", total_minor: total, reason: "goodwill", create_time: day(created),
  disposition: where === "toCredit" ? "CREDIT_NOTE_DISPOSITION_CREDIT" : "CREDIT_NOTE_DISPOSITION_REFUND",
  refund_state: where === "refunded" ? "REFUND_STATE_REFUNDED" : where === "refundFailed" ? "REFUND_STATE_FAILED" : "",
});
const delivery = (id: string, type: string, state: "SUCCEEDED" | "FAILED", created: number) => ({ id, event_id: `ev-${id}`, event_type: type, endpoint_id: "ep1", state: `WEBHOOK_DELIVERY_STATE_${state}`, attempts: 1, last_status: state === "FAILED" ? 500 : 200, last_error: "", create_time: day(created) });
const dead = (id: string, deliveries: number) => ({ message_id: id, delivery_count: deliveries, reason: "rejected", body_bytes: 120 });
const CASES: ListCase[] = [
  {
    name: "subscriptions",
    path: "/subscriptions",
    mock: get("/v1/subscriptions", { subscriptions: [sub("s1", "cus_mid", { start_time: day(12) }), sub("s2", "cus_old", { start_time: day(3), state: "SUBSCRIPTION_STATE_PAUSED" }), sub("s3", "cus_new", { start_time: day(20) })] }),
    rows: ["cus_mid", "cus_old", "cus_new"],
    sort: { value: "oldest", order: ["cus_old", "cus_mid", "cus_new"] },
    filter: { chip: "State", option: "paused", keep: ["cus_old"] },
    url: "",
  },
  {
    name: "invoices",
    path: "/invoices",
    mock: get(`/admin/v1/tenants/${TENANT}/invoices`, { invoices: [invoice("i1", "INV-1", "paid", "300", 1), invoice("i2", "INV-2", "open", "900", 2), invoice("i3", "INV-3", "paid", "100", 3)] }),
    rows: ["INV-1", "INV-2", "INV-3"],
    sort: { value: "amount_desc", order: ["INV-2", "INV-1", "INV-3"] },
    filter: { chip: "Status", option: "open", keep: ["INV-2"] },
    url: "",
  },
  {
    name: "expenses (accounting)",
    path: "/accounting",
    mock: all(
      get("/v1/expenses", { expenses: [expense("e1", "Hetzner", "infrastructure", "4000", "2026-10-10"), expense("e2", "Figma", "software", "9000", "2026-10-12"), expense("e3", "Google Ads", "marketing", "600", "2026-10-14")] }),
      get("/v1/accounting/summary", { from_date: "2026-10-01", to_date: "2026-10-31", granularity: "day", currencies: [] }),
    ),
    rows: ["Hetzner", "Figma", "Google Ads"],
    sort: { value: "amount_desc", order: ["Figma", "Hetzner", "Google Ads"] },
    filter: { chip: "Category", option: "Software", keep: ["Figma"] },
    url: "",
  },
  {
    name: "e-invoice accounts",
    path: "/e-invoice",
    mock: get(`/admin/v1/tenants/${TENANT}/e-invoice-accounts`, { accounts: [{ environment: "live", provider: "nilvera", mode: "production", updated_at: day(12) }, { environment: "sandbox", provider: "mock", mode: "test", updated_at: day(15) }] }),
    rows: ["nilvera", "mock"],
    sort: { value: "name_asc", order: ["mock", "nilvera"] },
    filter: { chip: "Environment", option: "sandbox", keep: ["mock"] },
    url: "",
  },
  {
    name: "operator tenants",
    open: async (page) => {
      await signInAsOperator(page);
    },
    mock: get("/admin/v1/tenants", { tenants: [tenantRow("t1", "Bravo", "ENVIRONMENT_LIVE", "TENANT_TIER_STANDARD", "TRY", 5), tenantRow("t2", "Alpha", "ENVIRONMENT_SANDBOX", "TENANT_TIER_FREE", "USD", 9), tenantRow("t3", "Charlie", "ENVIRONMENT_LIVE", "TENANT_TIER_FREE", "EUR", 2)] }),
    rows: ["Bravo", "Alpha", "Charlie"],
    sort: { value: "name_asc", order: ["Alpha", "Bravo", "Charlie"] },
    filter: { chip: "Environment", option: "sandbox", keep: ["Alpha"] },
    url: "f_",
  },
  {
    name: "meters",
    path: "/meters",
    mock: get("/v1/meters", { meters: METERS }),
    rows: ["api_calls", "storage_gb"],
    sort: { value: "name_desc", order: ["storage_gb", "api_calls"] },
    filter: { chip: "Aggregation", option: "Maximum", keep: ["storage_gb"] },
    url: "",
  },
  {
    name: "coupons",
    path: "/coupons",
    mock: get("/v1/coupons", { coupons: [coupon("WELCOME", true, 3), coupon("OLD", false, 1), coupon("SPRING", true, 8)] }),
    rows: ["WELCOME", "OLD", "SPRING"],
    sort: { value: "count_desc", order: ["SPRING", "WELCOME", "OLD"] },
    filter: { chip: "State", option: "Inactive", keep: ["OLD"] },
    url: "coupons-",
  },
  {
    name: "credit packs",
    path: "/credits",
    mock: all(get("/v1/credit-packs", { packs: [pack("p1", "Zeta", true, 1), pack("p2", "Alpha", false, 2), pack("p3", "Mid", true, 3)] }), get("/v1/customers", { customers: [] })),
    rows: ["Zeta", "Alpha", "Mid"],
    sort: { value: "name_asc", order: ["Alpha", "Mid", "Zeta"] },
    filter: { chip: "Status", option: "Withdrawn", keep: ["Alpha"] },
    url: "packs-",
  },
  {
    name: "webhook endpoints",
    path: "/webhooks",
    mock: get("/v1/webhook-endpoints", { endpoints: [endpoint("e1", "https://mid.example/hook", true, 12), endpoint("e2", "https://old.example/hook", false, 3), endpoint("e3", "https://new.example/hook", true, 20)] }),
    rows: ["mid.example", "old.example", "new.example"],
    sort: { value: "newest", order: ["new.example", "mid.example", "old.example"] },
    filter: { chip: "Status", option: "Paused", keep: ["old.example"] },
    url: "",
  },
  {
    name: "usage: meters tab",
    path: "/usage?tab=meters",
    mock: (gw) => gw.on(usageReads),
    rows: ["api_calls", "storage_gb"],
    sort: { value: "name_desc", order: ["storage_gb", "api_calls"] },
    filter: { chip: "Aggregation", option: "Highest value", keep: ["storage_gb"] },
    url: "meters-",
  },
  // --- reports: three tables on three tabs, each with its own prefix in the address
  {
    name: "reports: revenue",
    path: "/reports",
    mock: reports(),
    rows: ["USD", "TRY"],
    sort: { value: "amount_desc", order: ["TRY", "USD"] },
    filter: { chip: "Currency", option: "USD", keep: ["USD"] },
    url: "revenue-",
  },
  {
    name: "reports: subscriptions",
    path: "/reports?tab=subscriptions",
    mock: reports(),
    rows: ["EUR", "TRY", "USD"],
    sort: { value: "amount_desc", order: ["TRY", "USD", "EUR"] },
    filter: { chip: "Currency", option: "EUR", keep: ["EUR"] },
    url: "subs-",
  },
  {
    name: "reports: unpaid invoices",
    path: "/reports?tab=dunning",
    mock: reports(),
    rows: ["INV-D1", "INV-D2", "INV-D3"],
    sort: { value: "amount_desc", order: ["INV-D2", "INV-D3", "INV-D1"] },
    filter: { chip: "Currency", option: "USD", keep: ["INV-D3"] },
    url: "dunning-",
  },
  // --- the person's console
  {
    name: "payments (person)",
    person: true,
    path: `/app/t/${LIVE}/payments`,
    mock: get("/v1/payments", { payments: [payment("pay_a", "iyzico", "charge", "paid", "500", 3), payment("pay_b", "paddle", "refund", "refunded", "9700", 9), payment("pay_c", "iyzico", "charge", "failed", "200", 6)] }),
    // The rows show amounts, not ids.
    rows: ["5.00", "97.00", "2.00"],
    sort: { value: "amount_desc", order: ["97.00", "5.00", "2.00"] },
    filter: { chip: "Status", option: "Failed", keep: ["2.00"] },
    url: "",
  },
  {
    name: "members",
    person: true,
    path: "/app/members",
    mock: (gw) => gw,
    rows: ["Ada Lovelace", "Grace Hopper", "Linus Torvalds"],
    sort: { value: "name_desc", order: ["Linus Torvalds", "Grace Hopper", "Ada Lovelace"] },
    filter: { chip: "Role", option: "Viewer", keep: ["Linus Torvalds"] },
    url: "",
  },
  {
    name: "API keys",
    person: true,
    path: `/app/apps/${APP}/keys?env=live`,
    mock: (gw) => gw,
    rows: ["abcd1234", "oldkey00"],
    sort: { value: "oldest", order: ["oldkey00", "abcd1234"] },
    filter: { chip: "Status", option: "Revoked", keep: ["oldkey00"] },
    url: "f_",
  },
  {
    name: "integration accounts",
    person: true,
    path: `/app/apps/${APP}/integrations?env=live`,
    mock: (gw) => (liveGateway(gw, { accounts: [{ provider: "paddle", default: true, working: true, checked: true }, { provider: "iyzico", default: false, working: false, checked: true, problem: "refused" }] }), gw),
    scope: (page) => page.getByTestId("accounts-card"),
    item: "listitem",
    rows: ["iyzico", "Paddle"],
    filter: { chip: "Status", option: "Problem", keep: ["iyzico"] },
    url: "accounts-",
  },
  // --- the payments page holds four lists: the ledger, the review queue, the settlement and the fee rules
  {
    name: "payments: review queue",
    path: "/payments",
    mock: all(paymentsPage(), get(`/admin/v1/tenants/${TENANT}/reconciliation/discrepancies`, { discrepancies: [issue(1, "INV-R1", "paid_without_charge", "open", 500), issue(2, "INV-R2", "amount_mismatch", "explained", 700), issue(3, "INV-R3", "paid_without_charge", "open", 100)], counts: [], next_page_token: "" })),
    scope: (page) => page.getByTestId("review-queue"),
    rows: ["INV-R1", "INV-R2", "INV-R3"],
    sort: { value: "amount_desc", order: ["INV-R2", "INV-R1", "INV-R3"] },
    filter: { chip: "Kind", option: "Amount differs", keep: ["INV-R2"] },
    url: "issues-",
  },
  {
    name: "payments: settlement",
    path: "/payments",
    mock: all(paymentsPage(), get(`/admin/v1/tenants/${TENANT}/settlement/payments`, { period: "2026-10", payments: [settled("INV-S1", "pending_payout", 300), settled("INV-S2", "settled", 900), settled("INV-S3", "not_reported", 100)] })),
    scope: (page) => page.getByTestId("settlement-card"),
    rows: ["INV-S1", "INV-S2", "INV-S3"],
    sort: { value: "amount_desc", order: ["INV-S2", "INV-S1", "INV-S3"] },
    filter: { chip: "Status", option: "Settled", keep: ["INV-S2"] },
    url: "settlement-",
  },
  {
    name: "payments: fee rules",
    path: "/payments",
    mock: all(paymentsPage(), get(`/admin/v1/tenants/${TENANT}/settlement/fee-rules`, { rules: [feeRule("iyzico", "TRY", 12), feeRule("paddle", "USD", 18), feeRule("stripe", "EUR", 15)] })),
    scope: (page) => page.getByTestId("fee-rules-card"),
    rows: ["iyzico", "Paddle", "Stripe"],
    sort: { value: "name_desc", order: ["Stripe", "Paddle", "iyzico"] },
    filter: { chip: "Provider", option: "Paddle", keep: ["Paddle"] },
    url: "fee-",
  },
  // --- providers
  {
    name: "providers: accounts",
    path: "/providers",
    mock: all(providersPage(), get(`/admin/v1/tenants/${TENANT}/provider-accounts`, { accounts: [account("paddle", "live", 12), account("iyzico", "sandbox", 20), account("stripe", "live", 3)] })),
    scope: (page) => page.getByTestId("providers-accounts"),
    rows: ["paddle", "iyzico", "stripe"],
    sort: { value: "newest", order: ["iyzico", "paddle", "stripe"] },
    filter: { chip: "Environment", option: "sandbox", keep: ["iyzico"] },
    url: "accounts-",
  },
  {
    name: "providers: subscriptions the providers run",
    path: "/providers",
    mock: all(providersPage(), get(`/admin/v1/tenants/${TENANT}/provider-subscriptions`, { subscriptions: [providerSub("sub_mid", "active", 12), providerSub("sub_old", "paused", 3), providerSub("sub_new", "active", 20)] })),
    scope: (page) => page.getByTestId("providers-runs"),
    rows: ["sub_mid", "sub_old", "sub_new"],
    sort: { value: "oldest", order: ["sub_old", "sub_mid", "sub_new"] },
    filter: { chip: "State", option: "paused", keep: ["sub_old"] },
    url: "runs-",
  },
  {
    name: "dead-letter queues",
    open: async (page) => {
      await signInAsOperator(page);
      await page.goto("/dead-letters");
    },
    mock: get("/admin/v1/dead-letters", { queues: [{ queue: "alpha", messages: "2" }, { queue: "beta", messages: "30" }, { queue: "gamma", messages: "11" }], messages: [] }),
    rows: ["alpha.dlq", "beta.dlq", "gamma.dlq"],
    sort: { value: "count_desc", order: ["beta.dlq", "gamma.dlq", "alpha.dlq"] },
    url: "",
  },
  {
    name: "packages",
    path: "/packages",
    mock: (gw) =>
      gw.on((c) => {
        if (c.method !== "GET") return undefined;
        if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "zeta", name: "Zeta", latest_version: 1 }, { id: "p2", code: "alpha", name: "Alpha", latest_version: 1 }, { id: "p3", code: "mid", name: "Mid", latest_version: 1 }] } };
        const m = /^\/v1\/plans\/(\w+)\/versions\/latest$/.exec(c.path);
        return m ? { body: { id: `v-${m[1]}`, plan_code: m[1], version: 1, base_fee_minor: "1000", fees: [], prices: [], publish_time: day(m[1] === "mid" ? 20 : 5) } } : undefined;
      }),
    rows: ["Zeta", "Alpha", "Mid"],
    sort: { value: "name_asc", order: ["Alpha", "Mid", "Zeta"] },
    url: "",
  },
  {
    name: "withdrawals (contracts)",
    path: "/contracts?tab=withdrawals",
    mock: (gw) => (contractsGateway(gw, { withdrawals: [withdrawal("w1", "cus_mid", "received", "stmt one", { receive_time: day(12) }), withdrawal("w2", "cus_old", "accepted", "stmt two", { receive_time: day(3) }), withdrawal("w3", "cus_new", "received", "stmt three", { receive_time: day(20) })] }, TENANT), gw),
    rows: ["cus_mid", "cus_old", "cus_new"],
    sort: { value: "oldest", order: ["cus_old", "cus_mid", "cus_new"] },
    filter: { chip: "Status", option: "Accepted", keep: ["cus_old"] },
    url: "",
  },
  // --- credits: the balance of one customer, in two tabs
  {
    name: "credit lots of a customer",
    path: "/credits?customer=cus_1",
    mock: all(creditsPage(), get("/v1/customers/cus_1/credits", { balance: "666", lots: [lot("l1", "promo", "111", 5, 25), lot("l2", "manual", "222", 6, 22), lot("l3", "purchase", "333", 7, 28)] })),
    scope: (page) => page.getByTestId("credits-balance"),
    // The rows show how much is left of each lot.
    rows: ["111", "222", "333"],
    sort: { value: "expires_asc", order: ["222", "111", "333"] },
    filter: { chip: "Source", option: "Manual", keep: ["222"] },
    url: "lots-",
  },
  {
    name: "credit ledger of a customer",
    path: "/credits?customer=cus_1&view=ledger",
    mock: all(creditsPage(), get("/v1/customers/cus_1/credits/ledger", { entries: [entry("e1", "grant", "100", "alpha reason", 5), entry("e2", "consume", "-900", "bravo reason", 6), entry("e3", "grant", "400", "charlie reason", 7)], next_page_token: "" })),
    scope: (page) => page.getByTestId("credits-ledger"),
    rows: ["alpha reason", "bravo reason", "charlie reason"],
    sort: { value: "amount_desc", order: ["charlie reason", "alpha reason", "bravo reason"] },
    filter: { chip: "Type", option: "Spent", keep: ["bravo reason"] },
    url: "ledger-",
  },
  {
    name: "usage: customers tab",
    path: "/usage?tab=customers",
    mock: (gw) => gw.on(usageReads),
    rows: ["Busy Co", "Near Ltd", "Idle Inc"],
    sort: { value: "name_desc", order: ["Near Ltd", "Idle Inc", "Busy Co"] },
    filter: { chip: "Usage", option: "Without usage", keep: ["Idle Inc"] },
    url: "customers-",
  },
  {
    name: "support tickets (person)",
    person: true,
    path: "/app/support",
    mock: get("/app/v1/support/tickets", { tickets: [ticket("t1", 1001, "Webhook is late", 12), ticket("t2", 1002, "Invoice question", 3), ticket("t3", 1003, "Payment failed", 20)] }),
    rows: ["Webhook is late", "Invoice question", "Payment failed"],
    sort: { value: "oldest", order: ["Invoice question", "Webhook is late", "Payment failed"] },
    url: "",
  },
  // --- the lists that live beside or inside something else: an account's apps, the credit notes, a webhook's deliveries, a queue's messages, the provider's catalogue, the operator's inbox
  {
    name: "apps of an account",
    path: "/apps",
    tenant: { account_id: "acc-1" },
    mock: get("/admin/v1/accounts/acc-1/apps", { apps: [appRow("a1", "Mid", "mid", 12, "ENVIRONMENT_LIVE"), appRow("a2", "Alpha", "alpha", 3, "ENVIRONMENT_SANDBOX"), appRow("a3", "Zeta", "zeta", 20, "ENVIRONMENT_LIVE")] }),
    rows: ["Mid", "Alpha", "Zeta"],
    sort: { value: "name_asc", order: ["Alpha", "Mid", "Zeta"] },
    filter: { chip: "Environments", option: /sandbox/i, keep: ["Alpha"] },
    url: "f_",
  },
  {
    name: "credit notes",
    path: "/payments",
    mock: all(paymentsPage(), get("/v1/credit-notes", { credit_notes: [creditNote("n1", "CN-1", "300", "toCredit", 12), creditNote("n2", "CN-2", "900", "refunded", 3), creditNote("n3", "CN-3", "100", "refundFailed", 20)] })),
    scope: (page) => page.getByTestId("credit-notes-card"),
    rows: ["CN-1", "CN-2", "CN-3"],
    sort: { value: "amount_desc", order: ["CN-2", "CN-1", "CN-3"] },
    filter: { chip: "Went to", option: "Refund failed", keep: ["CN-3"] },
  },
  {
    name: "webhook deliveries",
    open: async (page) => {
      await openTenantPage(page, "/webhooks");
      await page.getByRole("row", { name: /example.com\/hook/ }).click();
    },
    mock: all(
      get("/v1/webhook-endpoints", { endpoints: [endpoint("ep1", "https://example.com/hook", true, 5)] }),
      get("/v1/webhook-deliveries", { deliveries: [delivery("d1", "invoice.paid", "SUCCEEDED", 12), delivery("d2", "customer.blocked", "FAILED", 3), delivery("d3", "invoice.voided", "SUCCEEDED", 20)] }),
    ),
    scope: (page) => page.getByRole("region", { name: "Deliveries" }),
    rows: ["invoice.paid", "customer.blocked", "invoice.voided"],
    sort: { value: "oldest", order: ["customer.blocked", "invoice.paid", "invoice.voided"] },
    filter: { chip: "Status", option: "Failed", keep: ["customer.blocked"] },
    url: "deliveries-",
  },
  {
    name: "messages of a dead-letter queue",
    open: async (page) => {
      await signInAsOperator(page);
      await page.goto("/dead-letters");
      await page.getByRole("row", { name: /alpha.dlq/ }).click();
    },
    mock: (gw) =>
      gw.on((c) =>
        c.method === "GET" && c.path === "/admin/v1/dead-letters"
          ? { body: { queues: [{ queue: "alpha", messages: "3" }], messages: c.query.get("queue") ? [dead("msg-a", 2), dead("msg-b", 9), dead("msg-c", 5)] : [] } }
          : undefined,
      ),
    scope: (page) => page.getByRole("dialog"),
    rows: ["msg-a", "msg-b", "msg-c"],
    sort: { value: "count_desc", order: ["msg-b", "msg-c", "msg-a"] },
  },
  {
    name: "the provider's catalogue",
    open: async (page) => {
      await openTenantPage(page, "/providers");
      await page.getByRole("form", { name: "Catalogue at the provider" }).getByLabel("Provider").selectOption("paddle");
    },
    mock: all(
      providersPage(),
      get("/v1/routing-rules", { rules: [] }),
      get("/v1/plans", { plans: [] }),
      get(`/admin/v1/tenants/${TENANT}/providers/paddle/catalogue`, { objects: [{ kind: "product", key: "plan:pro", ref: "pro_1" }, { kind: "price", key: "v1|month|USD", ref: "pri_1" }, { kind: "price", key: "v1|month|EUR", ref: "pri_2" }] }),
    ),
    scope: (page) => page.getByTestId("providers-catalogue"),
    rows: ["plan:pro", "v1|month|USD", "v1|month|EUR"],
    sort: { value: "name_asc", order: ["plan:pro", "v1|month|EUR", "v1|month|USD"] },
    filter: { chip: "Kind", option: "product", keep: ["plan:pro"] },
    // The choice of provider, environment and package is the card's own state, not the address's: a reload shows no catalogue, so the address is not tried.
  },
  {
    name: "the operator's support inbox",
    open: async (page) => {
      await signInAsOperator(page);
      await page.goto("/support");
    },
    // The inbox's filters are the server's: the mock answers the way the server does, so the list under test is the one the filter produced.
    mock: (gw) =>
      gw.on((c) => {
        if (c.method !== "GET" || c.path !== "/admin/v1/support/tickets") return undefined;
        const priority = c.query.get("priority");
        const all = [ticket("t1", 1001, "Webhook is late", 12), ticket("t2", 1002, "Invoice question", 3), ticket("t3", 1003, "Payment failed", 20)].map((t, i) => ({ ...t, priority: ["normal", "low", "high"][i] }));
        return { body: { tickets: all.filter((t) => !priority || t.priority === priority) } };
      }),
    rows: ["Webhook is late", "Invoice question", "Payment failed"],
    sort: { value: "priority_desc", order: ["Payment failed", "Webhook is late", "Invoice question"] },
    filter: { chip: "Priority", option: "High", keep: ["Payment failed"] },
    url: "",
    // The inbox's filters are the page's state sent to the server, not the address's: only the sort survives a reload.
    filterInUrl: false,
  },
];

const root = (page: Page, c: ListCase): Locator | Page => c.scope?.(page) ?? page;
/** The rows of the table the page shows, as far as they are among `known`, in the order shown. */
const orderOf = (where: Locator | Page, known: string[], item: "row" | "listitem" = "row") => async () => (await where.getByRole(item).allInnerTexts()).map((t) => known.find((k) => t.includes(k))).filter(Boolean);

async function openList(page: Page, c: ListCase): Promise<MockGateway> {
  const gw = c.mock(c.person ? productGateway(new MockGateway()).gw : baseTenant(new MockGateway(), c.tenant));
  await gw.install(page);
  if (c.open) await c.open(page);
  else if (c.person) {
    await signInAsAda(page);
    await page.goto(c.path ?? "");
  } else await openTenantPage(page, c.path ?? "");
  await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(c.rows);
  return gw;
}
const param = (page: Page, prefix: string, key: string) => new URL(page.url()).searchParams.get(prefix + key);

for (const c of CASES) {
  if (c.sort) {
    const { value, order } = c.sort;
    test(`${c.name}: one select orders the rows${c.url === undefined ? "" : ", the choice is in the address and survives a reload"}`, async ({ page }) => {
      await openList(page, c);
      await root(page, c).getByTestId("sort-select").first().selectOption(value);
      await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(order);
      if (c.url !== undefined) {
        await expect.poll(() => param(page, c.url!, "sort")).toBe(value);
        await page.reload();
        await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(order);
        await expect(root(page, c).getByTestId("sort-select").first()).toHaveValue(value);
      }
      await root(page, c).getByTestId("sort-select").first().selectOption("");
      await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(c.rows);
      if (c.url !== undefined) await expect.poll(() => param(page, c.url!, "sort")).toBeNull();
    });
  }

  if (c.filter) {
    const { chip, option, keep } = c.filter;
    test(`${c.name}: a filter is a draft until Apply, the list follows it after, and the chip's cross takes it off`, async ({ page }) => {
      await openList(page, c);
      const button = root(page, c).getByRole("button", { name: chip, exact: true });
      await button.click();
      const list = page.getByRole("listbox", { name: chip });
      await list.getByRole("option", { name: option, exact: typeof option === "string" }).click();
      // Only the draft has changed: every row is still there.
      await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(c.rows);
      await page.getByTestId("filter-apply").click();
      await list.waitFor({ state: "hidden" });
      await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(keep);
      if (c.url !== undefined && c.filterInUrl !== false) {
        await page.reload();
        await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(keep);
      }
      await root(page, c).getByRole("button", { name: `${chip}: ×` }).click();
      await expect.poll(orderOf(root(page, c), c.rows, c.item)).toEqual(c.rows);
    });
  }
}
