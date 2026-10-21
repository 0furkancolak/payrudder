import type { Page } from "@playwright/test";
import type { MockGateway } from "./gateway";

export const SESSION = "qs_" + "A".repeat(43);
export const APP = "a0000000-0000-4000-8000-000000000001";
export const LIVE = "a1000000-0000-4000-8000-000000000001";
export const SANDBOX = "a1000000-0000-4000-8000-000000000002";
export const ACCOUNT = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";

export const user = { id: "11111111-1111-4111-8111-111111111111", email: "ada@example.com", display_name: "Ada Lovelace", create_time: "2026-10-01T09:00:00Z", email_verified: true };
export const ACCOUNT2 = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
export const memberships = [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "owner" }];
const GRACE = "44444444-4444-4444-8444-444444444444";
const LINUS = "55555555-5555-4555-8555-555555555555";
export const member = (user_id: string, email: string, display_name: string, role: string) => ({ user_id, email, display_name, role, create_time: "2026-10-02T09:00:00Z" });
export const team = () => [
  member(user.id, user.email, user.display_name, "owner"),
  member(GRACE, "grace@example.com", "Grace Hopper", "admin"),
  member(LINUS, "linus@example.com", "Linus Torvalds", "viewer"),
];
export const invitation = (id: string, email: string, role: string, expire_time = "2099-10-28T09:00:00Z") => ({
  id, email, role, invited_by_email: user.email, invited_by_name: user.display_name, create_time: "2026-10-20T09:00:00Z", send_time: "2026-10-20T09:00:00Z", expire_time,
});

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
  /** Whether Ada's address is verified; a verification link flips it. */
  verified: boolean;
  /** Ada's role in the account, its members, open invitations and name; the memberships she has (a second one makes the account switcher appear). */
  role: string;
  members: ReturnType<typeof team>;
  invitations: ReturnType<typeof invitation>[];
  accountName: string;
  memberships: { account_id: string; account_name: string; role: string }[];
  /** The tokens the mocked mails carry. */
  verifyToken: string;
  resetToken: string;
}

/** A stand-in for the product API with a person signed in as Ada. `state` is the test's to change while it runs. */
export function productGateway(gw: MockGateway, state: Partial<ProductState> = {}): { gw: MockGateway; state: ProductState } {
  const s: ProductState = { apps: [app], keys: [...keys], overview: overview("live"), sessionOk: true, verified: true, verifyToken: "qv_good", resetToken: "qr_good",
    role: "owner", members: team(), invitations: [], accountName: "Ada Labs", memberships, ...state };
  const who = () => ({ ...user, email_verified: s.verified });
  const badLink = { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "identity.token.invalid", message: "bad link" } } };
  gw.on((c) => {
    if (c.path === "/auth/signin" && c.method === "POST") {
      return c.body?.password === "right password!" || c.body?.password === "a long enough password"
        ? { body: { user: who(), memberships: s.memberships, token: SESSION, expire_time: "2026-11-04T09:00:00Z" } }
        : { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.auth.invalid_credentials", message: "wrong" } } };
    }
    // Sign-up opens no session: 202 whatever the address. The page then signs in with the same password.
    if (c.path === "/auth/signup" && c.method === "POST") return { status: 202, body: { status: "accepted" } };
    if (c.path === "/auth/verify" && c.method === "POST") {
      if (c.body?.token !== s.verifyToken) return badLink;
      s.verified = true;
      s.verifyToken = "";
      return { body: { user: who() } };
    }
    if (c.path === "/auth/resend-verification" && c.method === "POST") return { status: 204, body: {} };
    if (c.path === "/auth/forgot" && c.method === "POST") return { status: 202, body: { status: "accepted" } };
    if (c.path === "/auth/reset" && c.method === "POST") {
      if (c.body?.token !== s.resetToken) return badLink;
      s.resetToken = "";
      return { status: 204, body: {} };
    }
    if (c.path === "/auth/signout") return { status: 200, body: {} };
    if (c.path.startsWith("/app/v1") || c.path === "/auth/me") {
      if (!s.sessionOk) return { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.session.invalid", message: "sign in again" } } };
    }
    if (c.path === "/auth/me") return { body: { user: who(), memberships: s.memberships, expire_time: "2026-11-04T09:00:00Z" } };
    if (c.path === "/app/v1/account" && c.method === "GET") {
      const id = c.query.get("account_id") ?? ACCOUNT;
      return { body: { id, name: id === ACCOUNT ? s.accountName : (s.memberships.find((m) => m.account_id === id)?.account_name ?? ""), role: s.role, create_time: "2026-10-01T09:00:00Z", members: s.members } };
    }
    if (c.path === "/app/v1/account" && c.method === "PATCH") {
      s.accountName = String(c.body?.name);
      return { body: { name: s.accountName } };
    }
    const forbidden = { status: 403, body: { error: { code: "PERMISSION_DENIED", key: "identity.account.forbidden", message: "no" } } };
    const lastOwner = { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "identity.member.last_owner", message: "keep an owner" } } };
    if (c.path === "/app/v1/account/invitations" && c.method === "GET") return s.role === "viewer" ? forbidden : { body: { invitations: s.invitations } };
    if (c.path === "/app/v1/account/invitations" && c.method === "POST") {
      if (s.role === "viewer") return forbidden;
      const email = String(c.body?.email);
      if (s.members.some((m) => m.email === email)) return { status: 409, body: { error: { code: "ALREADY_EXISTS", key: "identity.invite.already_member", message: "member" } } };
      const created = invitation(`inv-${s.invitations.length + 1}`, email, String(c.body?.role));
      s.invitations = [created, ...s.invitations];
      return { status: 201, body: created };
    }
    if (c.path.startsWith("/app/v1/account/invitations/") && c.method === "DELETE") {
      s.invitations = s.invitations.filter((i) => i.id !== c.path.split("/").pop());
      return { status: 204, body: {} };
    }
    if (c.path.startsWith("/app/v1/account/invitations/") && c.path.endsWith("/resend")) return { body: invitation(c.path.split("/")[5] ?? "", "x@example.com", "viewer") };
    if (c.path.startsWith("/app/v1/account/members/") && c.method === "PATCH") {
      const id = c.path.split("/").pop();
      const target = s.members.find((m) => m.user_id === id);
      if (target?.role === "owner" && c.body?.role !== "owner" && s.members.filter((m) => m.role === "owner").length === 1) return lastOwner;
      s.members = s.members.map((m) => (m.user_id === id ? { ...m, role: String(c.body?.role) } : m));
      return { body: { role: c.body?.role } };
    }
    if (c.path.startsWith("/app/v1/account/members/") && c.method === "DELETE") {
      const id = c.path.split("/").pop();
      const target = s.members.find((m) => m.user_id === id);
      if (target?.role === "owner" && s.members.filter((m) => m.role === "owner").length === 1) return lastOwner;
      s.members = s.members.filter((m) => m.user_id !== id);
      if (id === user.id) s.memberships = s.memberships.filter((m) => m.account_id !== ACCOUNT);
      return { status: 204, body: {} };
    }
    // The mailed invitation: qi_new is for an address without an account, qi_has for Ada's, qi_other for somebody else's who has one.
    if (c.path === "/auth/invitations/lookup") {
      const info = {
        qi_new: { email: "ned@example.com", has_account: false },
        qi_has: { email: "ada@example.com", has_account: true },
        qi_other: { email: "grace@example.com", has_account: true },
      }[String(c.body?.token)];
      return info
        ? { body: { account_name: "Ada Labs", role: "viewer", invited_by_name: "Ada Lovelace", ...info } }
        : { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "identity.token.invalid", message: "bad link" } } };
    }
    if (c.path === "/auth/invitations/accept") {
      return { body: { user: { ...user, email: "ned@example.com", display_name: String(c.body?.display_name ?? "") }, memberships, token: SESSION, expire_time: "2026-11-04T09:00:00Z" } };
    }
    if (c.path === "/app/v1/invitations/accept") return { body: { account_id: ACCOUNT, memberships } };
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
      if (live && !s.verified) return { status: 403, body: { error: { code: "PERMISSION_DENIED", key: "identity.email.not_verified", message: "confirm" } } };
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
