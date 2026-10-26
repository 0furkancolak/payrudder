import type { Page } from "@playwright/test";
import { dashboardFixture } from "./dashboard";
import type { MockGateway } from "./gateway";
import { usageReads } from "./usage";

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

export const tenant = (id: string, environment: "LIVE" | "SANDBOX", currency = "USD", status: "ACTIVE" | "INACTIVE" | "SUSPENDED" = "ACTIVE") => ({
  id,
  account_id: ACCOUNT,
  app_id: APP,
  environment: `ENVIRONMENT_${environment}`,
  status: `TENANT_STATUS_${status}`,
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
  /** What the dashboard answers: a fixed body, or a function of the query (period, compare, environment). Default: the multi-currency fixture. */
  dashboard: Record<string, unknown> | ((q: Record<string, string>) => { status?: number; body: unknown }) | null;
  sessionOk: boolean;
  /** Whether Ada's address is verified; a verification link flips it. */
  verified: boolean;
  /** Ada's role in the account, its members, open invitations and name; the memberships she has (a second one makes the account switcher appear). */
  role: string;
  members: ReturnType<typeof team>;
  invitations: ReturnType<typeof invitation>[];
  accountName: string;
  memberships: { account_id: string; account_name: string; role: string }[];
  /** How many accounts the person may have before creating another is refused (default 20). */
  maxAccounts?: number;
  /** The tokens the mocked mails carry. */
  verifyToken: string;
  resetToken: string;
  /** Phase 42: whether the server calls Ada a platform operator, which sign-in methods it offers, and the one-time code the mocked mail carries. */
  operator: boolean;
  methods: { password: boolean; otp: boolean; google: boolean };
  code: string;
}

/** A stand-in for the product API with a person signed in as Ada. `state` is the test's to change while it runs. */
export function productGateway(gw: MockGateway, state: Partial<ProductState> = {}): { gw: MockGateway; state: ProductState } {
  const s: ProductState = { apps: [app], keys: [...keys], overview: overview("live"), dashboard: null, sessionOk: true, verified: true, verifyToken: "qv_good", resetToken: "qr_good",
    role: "owner", members: team(), invitations: [], accountName: "Ada Labs", memberships, operator: false,
    methods: { password: true, otp: true, google: false }, code: "123456", ...state };
  const who = () => ({ ...user, email_verified: s.verified });
  const badLink = { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "identity.token.invalid", message: "bad link" } } };
  gw.on((c) => {
    if (c.path === "/auth/signin" && c.method === "POST") {
      return c.body?.password === "right password!" || c.body?.password === "a long enough password"
        ? { body: { user: who(), memberships: s.memberships, token: SESSION, operator: s.operator, expire_time: "2026-11-04T09:00:00Z" } }
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
    if (c.path === "/auth/methods" && c.method === "GET") return { body: s.methods };
    // The same answer for every address; the right code (s.code) signs in, anything else is the one refusal.
    if (c.path === "/auth/otp/request" && c.method === "POST") return { status: 202, body: { status: "accepted" } };
    if (c.path === "/auth/otp/verify" && c.method === "POST") {
      return c.body?.code === s.code
        ? { body: { user: who(), memberships: s.memberships, token: SESSION, operator: s.operator, expire_time: "2026-11-04T09:00:00Z" } }
        : { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "identity.otp.invalid", message: "wrong code" } } };
    }
    if (c.path.startsWith("/app/v1") || c.path === "/auth/me") {
      if (!s.sessionOk) return { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.session.invalid", message: "sign in again" } } };
    }
    if (c.path === "/auth/me") return { body: { user: who(), memberships: s.memberships, operator: s.operator, expire_time: "2026-11-04T09:00:00Z" } };
    if (c.path === "/app/v1/account" && c.method === "GET") {
      const id = c.query.get("account_id") ?? ACCOUNT;
      return { body: { id, name: id === ACCOUNT ? s.accountName : (s.memberships.find((m) => m.account_id === id)?.account_name ?? ""), role: s.role, create_time: "2026-10-01T09:00:00Z", members: s.members } };
    }
    if (c.path === "/app/v1/accounts" && c.method === "POST") {
      const name = String(c.body?.name ?? "").trim();
      if (!name) return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "common.invalid_argument", message: "name" } } };
      if (s.memberships.length >= (s.maxAccounts ?? 20)) return { status: 429, body: { error: { code: "RESOURCE_EXHAUSTED", key: "identity.account.quota", message: "too many" } } };
      const made = { account_id: `dddddddd-dddd-4ddd-8ddd-${String(s.memberships.length).padStart(12, "0")}`, account_name: name, role: "owner" };
      s.memberships = [...s.memberships, made];
      return { status: 201, body: made };
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
    if (c.path === `/app/v1/apps/${APP}` && c.method === "GET") return { body: { app: (s.apps as { id: string }[]).find((a) => a.id === APP) ?? app, role: s.role } };
    if (c.path === `/app/v1/apps/${APP}/dashboard`) {
      const q = Object.fromEntries(c.query.entries());
      if (typeof s.dashboard === "function") return s.dashboard(q);
      return { body: s.dashboard ?? dashboardFixture(q) };
    }
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

/** The app of the fixture with its live environment in the given status (inactive: just created, nothing set up yet). */
export const appWithLive = (status: "ACTIVE" | "INACTIVE" | "SUSPENDED") => ({ ...app, tenants: [tenant(LIVE, "LIVE", "USD", status), tenant(SANDBOX, "SANDBOX")] });

export interface LiveState {
  done: Record<string, boolean>;
  status: "ACTIVE" | "INACTIVE" | "SUSPENDED";
  /** The reset job the gateway reports (null: never reset), and what the promote preview shows. */
  reset: Record<string, unknown> | null;
  changes: { family: string; key: string; action: string; detail?: string; secret?: string }[];
  hash: string;
  /** When set, applying a promote answers 409 stale. */
  stale: boolean;
  /** The live provider accounts payments-worker reports, and the status and key a new account is refused with, when set. */
  accounts: { provider: string; default: boolean; working: boolean; checked: boolean; problem?: string }[];
  refuseProvider: { status: number; key: string } | null;
  /** The billing profile the owner saved. */
  profile: Record<string, unknown> | null;
}

const goLiveBody = (s: LiveState) => {
  const items = ["email_verified", "legal_profile", "payment_provider", "plan_published", "terms_accepted"].map((key) => ({ key, done: s.done[key] === true }));
  return { tenant_id: LIVE, status: `TENANT_STATUS_${s.status}`, ready: items.every((i) => i.done), items };
};

/** The go-live checklist, promote, sandbox reset and key rotation of the product API (phase 45). `state` is the test's to change while it runs. */
export function liveGateway(gw: MockGateway, state: Partial<LiveState> = {}): { gw: MockGateway; state: LiveState } {
  const s: LiveState = { done: { email_verified: true }, status: "INACTIVE", reset: null, changes: [], hash: "h1", stale: false, accounts: [], refuseProvider: null, profile: null, ...state };
  gw.on((c) => {
    const providers = `/app/v1/apps/${APP}/go-live/providers`;
    if (c.path === `/app/v1/apps/${APP}/go-live/terms` && c.method === "POST") {
      s.done.terms_accepted = true;
      return { body: { accept_time: "2026-10-21T10:00:00Z" } };
    }
    if (c.path === `/app/v1/apps/${APP}/billing-profile` && c.method === "PUT") {
      s.profile = c.body;
      s.done.legal_profile = true;
      return { body: { id: LIVE, ...c.body } };
    }
    if (c.path === providers && c.method === "GET") return { body: { ready: s.accounts.some((a) => a.working), providers: s.accounts.filter((a) => a.working).map((a) => a.provider), accounts: s.accounts } };
    if (c.path.startsWith(`${providers}/`) && c.method === "PUT") {
      if (s.refuseProvider) return { status: s.refuseProvider.status, body: { error: { code: "INVALID_ARGUMENT", key: s.refuseProvider.key, message: "refused" } } };
      const provider = c.path.split("/").pop() ?? "";
      s.accounts = [...s.accounts.filter((a) => a.provider !== provider), { provider, default: s.accounts.length === 0, working: true, checked: provider !== "banktransfer" }];
      s.done.payment_provider = true;
      return { body: { provider, environment: "live", default: s.accounts.length === 1 } };
    }
    if (c.path.startsWith(`${providers}/`) && c.method === "DELETE") {
      s.accounts = s.accounts.filter((a) => a.provider !== c.path.split("/").pop());
      s.done.payment_provider = s.accounts.some((a) => a.working);
      return { status: 204, body: {} };
    }
    if (c.path === `/app/v1/apps/${APP}/go-live` && c.method === "GET") return { body: { go_live: goLiveBody(s) } };
    if (c.path === `/app/v1/apps/${APP}/go-live` && c.method === "POST") {
      const body = goLiveBody({ ...s, done: { ...s.done, terms_accepted: s.done.terms_accepted || c.body?.accept_terms === true } });
      if (!body.ready) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "identity.go_live.incomplete", message: "not ready" } } };
      s.status = "ACTIVE";
      s.done.terms_accepted = true;
      return { body: { go_live: goLiveBody(s) } };
    }
    if (c.path === `/app/v1/apps/${APP}/promote` && c.method === "POST") {
      const create = s.changes.filter((x) => x.action === "ACTION_CREATE").length;
      const dry = c.body?.dry_run === true;
      if (!dry && (s.stale || c.body?.plan_hash !== s.hash)) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "identity.promote.stale", message: "moved" } } };
      return {
        body: {
          dry_run: dry, plan_hash: s.hash, created: create, unchanged: s.changes.filter((x) => x.action === "ACTION_UNCHANGED").length,
          conflicts: s.changes.filter((x) => x.action === "ACTION_CONFLICT").length, skipped: 0, failed: 0,
          changes: s.changes.map((x) => ({ ...x, applied: !dry && x.action === "ACTION_CREATE" })),
        },
      };
    }
    if (c.path === `/app/v1/apps/${APP}/sandbox/reset` && c.method === "GET") return { body: s.reset ? { sandbox_reset: s.reset } : {} };
    if (c.path === `/app/v1/apps/${APP}/sandbox/reset` && c.method === "POST") {
      const steps = ["plans", "ingest", "aggregator", "analytics", "billing", "payments"].map((name) => ({ name, state: "done", deleted: name === "plans" ? { customers: 4, subscriptions: 3 } : {} }));
      s.reset = { id: "r1", state: "done", kind: "manual", steps, create_time: "2026-10-21T10:00:00Z", finish_time: "2026-10-21T10:00:05Z" };
      return { status: 202, body: { sandbox_reset: s.reset } };
    }
    if (c.path.startsWith(`/app/v1/apps/${APP}/api-keys/`) && c.path.endsWith("/rotate") && c.method === "POST") {
      const key = { id: "k10", tenant_id: SANDBOX, prefix: "rotated1", environment: "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE", scope: "SCOPE_FULL", create_time: "2026-10-21T10:00:00Z" };
      return { status: 201, body: { api_key: key, plaintext: "qk_test_rotated1_SECRETSECRETSECRETSECRET", previous: { id: c.path.split("/")[6], expire_time: "2026-10-22T10:00:00Z" } } };
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

export const INVOICE_1 = "d0000000-0000-4000-8000-000000000001";
export const INVOICE_2 = "d0000000-0000-4000-8000-000000000002";
export const INVOICE_3 = "d0000000-0000-4000-8000-000000000003";
const invoice = (id: string, number: string, status: string, total: string, customer: string, extra: Record<string, unknown> = {}) => ({
  id, number, status, currency: "USD", total_minor: total, subtotal_minor: total, customer_external_id: customer, period_id: "2026-09", kind: "REGULAR",
  create_time: "2026-10-01T09:00:00Z", finalize_time: "2026-10-01T09:05:00Z", saga_state: "COMPLETED", ...extra,
});
export const invoiceLines = [
  { line_no: 1, kind: "BASE_FEE", description: "Pro plan, September", service_from: "2026-09-01T00:00:00Z", service_to: "2026-09-30T00:00:00Z", quantity: "1", amount_minor: "4000" },
  { line_no: 2, kind: "USAGE", description: "API calls", meter_code: "api_calls", quantity: "9000", amount_minor: "900" },
];

/** What the billing pages of the product app read: /v1/invoices, /v1/payments, packages and pricing, reports. Nothing here is an /admin route. */
export function billingGateway(gw: MockGateway): MockGateway {
  return gw.on((c) => {
    if (c.method !== "GET" || !c.path.startsWith("/v1/")) return undefined;
    if (c.path === "/v1/invoices") {
      const status = c.query.get("status");
      if (c.query.get("page_token") === "p2") return { body: { invoices: [invoice(INVOICE_3, "INV-2026-000001", "paid", "1500", "cus_old")] } };
      const all = [invoice(INVOICE_1, "INV-2026-000003", "paid", "4900", "cus_acme"), invoice(INVOICE_2, "INV-2026-000002", "open", "29900", "cus_globex")];
      return { body: { invoices: status ? all.filter((i) => i.status === status) : all, next_page_token: status ? "" : "p2" } };
    }
    if (c.path === `/v1/invoices/${INVOICE_1}`) return { body: invoice(INVOICE_1, "INV-2026-000003", "paid", "4900", "cus_acme", { lines: invoiceLines, payments: [] }) };
    if (c.path === `/v1/invoices/${INVOICE_1}/pdf`) return { body: { url: "https://files.example.test/INV-2026-000003.pdf", expires_at: "2026-10-21T10:05:00Z" } };
    if (c.path === "/v1/payments") {
      return {
        body: {
          truncated: false,
          payments: [
            { id: "9", invoice_id: INVOICE_1, provider: "iyzico", kind: "charge", status: "paid", amount_minor: "4900", currency: "USD", occurred_at: "2026-10-21T09:41:00Z" },
            { id: "8", invoice_id: INVOICE_2, provider: "iyzico", kind: "charge", status: "failed", amount_minor: "29900", currency: "USD", occurred_at: "2026-10-21T08:12:00Z" },
            { id: "7", invoice_id: INVOICE_3, provider: "paddle", kind: "charge", status: "processing", amount_minor: "1500", currency: "USD", occurred_at: "2026-10-20T17:03:00Z" },
            { id: "6", invoice_id: INVOICE_1, provider: "iyzico", kind: "refund", status: "refunded", amount_minor: "900", currency: "USD", occurred_at: "2026-10-19T15:20:00Z" },
          ],
        },
      };
    }
    if (c.path === "/v1/plans") return { body: { plans: [{ id: "p1", code: "pro", name: "Pro", latest_version: 2 }] } };
    if (c.path === "/v1/plans/pro/versions/latest") return { body: { version: 2, base_fee_minor: "4000", fees: [{ currency: "EUR", interval: "SUBSCRIPTION_INTERVAL_YEAR", base_fee_minor: "40000" }], prices: [{ meter_code: "api_calls" }] } };
    if (c.path === "/v1/coupons") return { body: { coupons: [{ code: "SAVE10", percent_off_bps: 1000, active: true, redeemed: 3, duration: "COUPON_DURATION_ONCE" }] } };
    if (c.path === "/v1/coupon-redemptions") return { body: { redemptions: [], total: 0 } };
    if (c.path === "/v1/tax-rates") return { body: { rates: [{ country: "DE", name: "VAT", rate_bps: 1900 }] } };
    if (c.path === "/v1/dunning-settings") return { body: { retry_hours: [24, 72, 168], defaults_in_use: true } };
    if (c.path === "/v1/reports/revenue") return { body: { rows: [{ period: "2026-09", currency: "USD", billed_minor: "100000", collected_minor: "90000", outstanding_minor: "10000", refunded_minor: "0" }] } };
    if (c.path === "/v1/reports/subscriptions") return { body: { rows: [{ currency: "USD", active: 4, trialing: 0, past_due: 1, mrr_minor: "490000" }] } };
    if (c.path === "/v1/reports/dunning") return { body: { rows: [] } };
    if (c.path === "/v1/customers") return { body: { customers: [] } };
    if (c.path === "/v1/promotion-codes") return { body: { codes: [], total: 0 } };
    return usageReads(c);
  });
}
