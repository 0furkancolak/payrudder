import type { Page } from "@playwright/test";
import type { MockGateway } from "./gateway";

export const SESSION = "qs_" + "A".repeat(43);
export const APP = "a0000000-0000-4000-8000-000000000001";
export const LIVE = "a1000000-0000-4000-8000-000000000001";
export const SANDBOX = "a1000000-0000-4000-8000-000000000002";
export const ACCOUNT = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";

export const user = { id: "11111111-1111-4111-8111-111111111111", email: "ada@example.com", display_name: "Ada Lovelace", create_time: "2026-10-01T09:00:00Z" };
export const memberships = [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "owner" }];

export const tenant = (id: string, environment: "LIVE" | "SANDBOX", currency = "USD") => ({
  id,
  account_id: ACCOUNT,
  app_id: APP,
  environment: `ENVIRONMENT_${environment}`,
  name: environment === "LIVE" ? "Web shop" : "Web shop (sandbox)",
  tier: "TENANT_TIER_FREE",
  currency,
  timezone: "Europe/Istanbul",
});

export const app = {
  id: APP,
  account_id: ACCOUNT,
  name: "Web shop",
  slug: "web-shop",
  create_time: "2026-10-02T09:00:00Z",
  tenants: [tenant(LIVE, "LIVE"), tenant(SANDBOX, "SANDBOX")],
};

const points = (values: number[]) => values.map((v, i) => ({ date: `2026-09-${String(22 + i).padStart(2, "0")}`, amount_minor: v }));

export function overview(env: "live" | "sandbox" = "live", extra: Record<string, unknown> = {}) {
  return {
    app_id: APP,
    environment: env,
    tenant_id: env === "live" ? LIVE : SANDBOX,
    currency: "USD",
    window_days: 30,
    generated_at: "2026-10-21T10:00:00Z",
    status: "attention",
    unavailable: [],
    payments: {
      truncated: false,
      by_status: [
        { status: "paid", count: 128 },
        { status: "processing", count: 6 },
        { status: "failed", count: 9 },
        { status: "refunded", count: 3 },
      ],
      by_currency: [
        { currency: "USD", paid_count: 118, paid_minor: 1284050, failed_count: 8, failed_minor: 39900, pending_count: 5, pending_minor: 24500, refunded_minor: 9900 },
        { currency: "TRY", paid_count: 10, paid_minor: 1850000, failed_count: 1, failed_minor: 25000, pending_count: 1, pending_minor: 9000, refunded_minor: 0 },
      ],
      series: [
        { currency: "USD", points: points([12000, 30000, 18000, 42000, 36000, 51000, 47000, 60000, 38000, 72000, 66000, 81000]) },
        { currency: "TRY", points: points([0, 0, 90000, 0, 125000, 0, 0, 240000, 0, 0, 310000, 0]) },
      ],
      recent: [
        { id: "9", invoice_id: "inv1", provider: "iyzico", kind: "charge", status: "paid", amount_minor: 4900, currency: "USD", occurred_at: "2026-10-21T09:41:00Z" },
        { id: "8", invoice_id: "inv2", provider: "iyzico", kind: "charge", status: "failed", amount_minor: 15000, currency: "USD", occurred_at: "2026-10-21T08:12:00Z" },
        { id: "7", invoice_id: "inv3", provider: "paddle", kind: "charge", status: "processing", amount_minor: 29900, currency: "USD", occurred_at: "2026-10-20T17:03:00Z" },
        { id: "6", invoice_id: "inv4", provider: "iyzico", kind: "charge", status: "paid", amount_minor: 250000, currency: "TRY", occurred_at: "2026-10-20T11:30:00Z" },
        { id: "5", invoice_id: "inv5", provider: "paddle", kind: "refund", status: "refunded", amount_minor: 4900, currency: "USD", occurred_at: "2026-10-19T15:20:00Z" },
      ],
    },
    invoices: {
      open: { count: 12, more: false },
      paid: { count: 100, more: true },
      revenue: [
        { currency: "TRY", billed_minor: 2400000, collected_minor: 1850000, outstanding_minor: 550000, refunded_minor: 0 },
        { currency: "USD", billed_minor: 1500000, collected_minor: 1284050, outstanding_minor: 215950, refunded_minor: 9900 },
      ],
      recent: [
        { id: "i1", number: "INV-2026-000412", customer_external_id: "cus_acme", status: "paid", currency: "USD", total_minor: 4900, create_time: "2026-10-21T09:00:00Z" },
        { id: "i2", number: "INV-2026-000411", customer_external_id: "cus_globex", status: "open", currency: "USD", total_minor: 29900, create_time: "2026-10-20T09:00:00Z" },
        { id: "i3", number: "INV-2026-000410", customer_external_id: "cus_initech", status: "uncollectible", currency: "TRY", total_minor: 90000, create_time: "2026-10-19T09:00:00Z" },
      ],
    },
    customers: { count: 214, more: false },
    subscriptions: [{ currency: "USD", active: 96, trialing: 4, past_due: 3, mrr_minor: 482000, new_mrr_minor: 39000, churned_mrr_minor: 9800 }],
    health: { payments_reachable: true, webhook_endpoints: 1, webhook_failed: 2, webhook_failed_more: false, providers: [{ provider: "iyzico", environment: "live", is_default: true }] },
    ...extra,
  };
}

export const keys = [
  { id: "k1", tenant_id: LIVE, prefix: "abcd1234", environment: "ENVIRONMENT_LIVE", status: "STATUS_ACTIVE", create_time: "2026-10-03T09:00:00Z" },
  { id: "k2", tenant_id: SANDBOX, prefix: "wxyz5678", environment: "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE", create_time: "2026-10-03T09:05:00Z" },
  { id: "k3", tenant_id: LIVE, prefix: "oldkey00", environment: "ENVIRONMENT_LIVE", status: "STATUS_REVOKED", create_time: "2026-09-12T09:00:00Z" },
];

export interface ProductState {
  apps: unknown[];
  keys: typeof keys;
  overview: Record<string, unknown>;
  sessionOk: boolean;
}

/** A stand-in for the product API with a person signed in as Ada. `state` is the test's to change while it runs. */
export function productGateway(gw: MockGateway, state: Partial<ProductState> = {}): { gw: MockGateway; state: ProductState } {
  const s: ProductState = { apps: [app], keys: [...keys], overview: overview("live"), sessionOk: true, ...state };
  gw.on((c) => {
    if (c.path === "/auth/signin" && c.method === "POST") {
      return c.body?.password === "right password!"
        ? { body: { user, memberships, token: SESSION, expire_time: "2026-11-04T09:00:00Z" } }
        : { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.auth.invalid_credentials", message: "wrong" } } };
    }
    if (c.path === "/auth/signup" && c.method === "POST") return { status: 201, body: { user: { ...user, email: c.body?.email }, memberships, token: SESSION, expire_time: "2026-11-04T09:00:00Z" } };
    if (c.path === "/auth/signout") return { status: 200, body: {} };
    if (c.path.startsWith("/app/v1") || c.path === "/auth/me") {
      if (!s.sessionOk) return { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.session.invalid", message: "sign in again" } } };
    }
    if (c.path === "/auth/me") return { body: { user, memberships, expire_time: "2026-11-04T09:00:00Z" } };
    if (c.path === "/app/v1/account") {
      return { body: { id: ACCOUNT, name: "Ada Labs", role: "owner", create_time: "2026-10-01T09:00:00Z", members: [{ user_id: user.id, email: user.email, display_name: user.display_name, role: "owner", create_time: "2026-10-01T09:00:00Z" }] } };
    }
    if (c.path === "/app/v1/apps" && c.method === "GET") return { body: { apps: s.apps } };
    if (c.path === "/app/v1/apps" && c.method === "POST") {
      const created = { ...app, id: "b0000000-0000-4000-8000-000000000002", name: String(c.body?.name), slug: String(c.body?.name).toLowerCase().replace(/\s+/g, "-") };
      s.apps = [...s.apps, created];
      return { status: 201, body: created };
    }
    if (c.path === `/app/v1/apps/${APP}` && c.method === "GET") return { body: { app, role: "owner" } };
    if (c.path.endsWith("/overview")) return { body: c.query.get("environment") === "sandbox" ? overview("sandbox") : s.overview };
    if (c.path === `/app/v1/apps/${APP}/api-keys` && c.method === "GET") return { body: { api_keys: s.keys } };
    if (c.path === `/app/v1/apps/${APP}/api-keys` && c.method === "POST") {
      const live = c.body?.environment === "live";
      const key = { id: "k9", tenant_id: live ? LIVE : SANDBOX, prefix: "newkey99", environment: live ? "ENVIRONMENT_LIVE" : "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE", create_time: "2026-10-21T10:00:00Z" };
      s.keys = [key, ...s.keys];
      return { status: 201, body: { api_key: key, plaintext: `qk_${live ? "live" : "test"}_newkey99_SECRETSECRETSECRETSECRET` } };
    }
    if (c.path.startsWith(`/app/v1/apps/${APP}/api-keys/`) && c.method === "DELETE") {
      const id = c.path.split("/").pop();
      s.keys = s.keys.map((k) => (k.id === id ? { ...k, status: "STATUS_REVOKED" } : k));
      return { status: 200, body: {} };
    }
    return undefined;
  });
  return { gw, state: s };
}

/** Signs in through the page, as a person would. */
export async function signInAsAda(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("E-mail").fill("ada@example.com");
  await page.getByLabel("Password", { exact: true }).fill("right password!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/app");
}
