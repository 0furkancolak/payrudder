import type { MockGateway } from "./gateway";

// A stand-in for the plan endpoints (phase 55): the plan of the signed-in person's account, its invoices, and the operator's organisations. The state is
// the test's to change while it runs.

export const PLATFORM = "f0000000-0000-4000-8000-0000000000f0";
export const INVOICE = "d1000000-0000-4000-8000-000000000001";
const ACCOUNT = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";

interface Spec {
  code: string;
  name: string;
  fee: number;
  limits: Record<string, number>;
}
const SPECS: Spec[] = [
  { code: "free", name: "Free", fee: 0, limits: { events: 10000, apps: 1, members: 2, api_keys: 4 } },
  { code: "pro", name: "Pro", fee: 2900, limits: { events: 1000000, apps: 5, members: 10, api_keys: 20 } },
  { code: "scale", name: "Scale", fee: 9900, limits: { events: 10000000, apps: 0, members: 0, api_keys: 0 } },
];

export interface SaasState {
  enabled: boolean;
  plan: string;
  state: string;
  role: string;
  /** Usage of apps, members, api keys and events. */
  used: { events: number; apps: number; members: number; api_keys: number };
  invoices: { id: string; number: string; status: string; currency: string; total_minor: string; create_time: string; finalize_time: string }[];
  organisations: Record<string, unknown>[];
}

export function plan(s: SaasState) {
  const spec = SPECS.find((x) => x.code === s.plan) ?? SPECS[0]!;
  const offer = (x: Spec) => ({
    code: x.code, name: x.name, version: 1, base_fee_minor: x.fee, currency: "USD", interval: "month", trial_days: x.fee > 0 ? 14 : 0, current: x.code === spec.code,
    limits: Object.entries(x.limits).map(([resource, limit]) => ({ resource, limit, mode: resource === "events" && x.code === "scale" ? "soft" : "hard" })),
  });
  return {
    enabled: s.enabled,
    ...(s.enabled
      ? {
          account_id: ACCOUNT,
          plan: offer(spec),
          state: s.state,
          ...(s.state === "trialing" ? { trial_end_time: "2026-11-09T09:00:00Z" } : {}),
          overridden: false,
          can_manage: s.role === "owner" || s.role === "admin",
          quotas: Object.entries(spec.limits).map(([resource, limit]) => {
            const used = s.used[resource as keyof SaasState["used"]];
            return { resource, used, limit, unlimited: limit === 0, mode: "hard", over_limit: limit > 0 && used >= limit, overridden: false, ...(resource === "events" ? { resets_time: "2026-11-01T00:00:00Z" } : {}) };
          }),
          offers: SPECS.map(offer),
        }
      : {}),
  };
}

export function saasGateway(gw: MockGateway, state: Partial<SaasState> = {}): { gw: MockGateway; state: SaasState } {
  const s: SaasState = {
    enabled: true, plan: "free", state: "active", role: "owner", used: { events: 9100, apps: 1, members: 2, api_keys: 3 },
    invoices: [{ id: INVOICE, number: "INV-2026-000010", status: "open", currency: "USD", total_minor: "2900", create_time: "2026-10-20T09:00:00Z", finalize_time: "2026-10-20T09:05:00Z" }],
    organisations: [
      { account_id: ACCOUNT, name: "Ada Labs", owner_email: "ada@example.com", plan_code: "free", plan_name: "Free", state: "active", overridden: false, apps: 1, members: 2, api_keys: 3, events_used: 9100, events_limit: 10000, create_time: "2026-10-01T09:00:00Z" },
    ],
    ...state,
  };
  gw.on((c) => {
    if (c.path === "/app/v1/account/plan" && c.method === "GET") return { body: plan(s) };
    if (c.path === "/app/v1/account/plan" && c.method === "PUT") {
      const target = SPECS.find((x) => x.code === c.body?.plan_code);
      if (!target) return { status: 404, body: { error: { code: "NOT_FOUND", key: "plans.saas.unknown_plan", message: "no such plan" } } };
      if (target.code === s.plan) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "plans.saas.same_plan", message: "same" } } };
      const doesNotFit = Object.entries(target.limits).some(([r, limit]) => r !== "events" && limit > 0 && s.used[r as keyof SaasState["used"]] > limit);
      if (doesNotFit) return { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "plans.saas.downgrade_blocked", message: "does not fit" } } };
      s.state = target.fee > 0 ? "trialing" : "active";
      s.plan = target.code;
      return { body: plan(s) };
    }
    if (c.path === "/app/v1/account/plan/invoices" && c.method === "GET") return { body: { invoices: s.invoices } };
    if (c.path === `/app/v1/account/plan/invoices/${INVOICE}/pay-link` && c.method === "POST") return { status: 201, body: { url: "https://pay.example.test/i/tok_plan" } };
    if (c.path === "/app/v1/account/plan/portal-link" && c.method === "POST") return { status: 201, body: { url: "https://pay.example.test/c/tok_portal", expires_at: "2026-10-22T09:00:00Z" } };
    if (c.path === "/admin/v1/saas") return { body: { enabled: s.enabled, tenant_id: PLATFORM, environment: "live", currency: "USD" } };
    if (c.path === "/admin/v1/organizations" && c.method === "GET") {
      const q = (c.query.get("q") ?? "").toLowerCase();
      return { body: { organizations: s.organisations.filter((o) => !q || String(o.name).toLowerCase().includes(q)), next_page_token: "" } };
    }
    const set = /^\/admin\/v1\/organizations\/([^/]+)\/plan$/.exec(c.path);
    if (set && c.method === "PUT") {
      s.organisations = s.organisations.map((o) =>
        o.account_id === set[1]
          ? { ...o, plan_code: c.body?.plan_code ?? o.plan_code, plan_name: SPECS.find((x) => x.code === c.body?.plan_code)?.name ?? o.plan_name, overridden: c.body?.limits ? true : o.overridden, events_limit: (c.body?.limits as Record<string, number> | undefined)?.events ?? o.events_limit }
          : o,
      );
      return { body: plan(s) };
    }
    const clear = /^\/admin\/v1\/organizations\/([^/]+)\/overrides$/.exec(c.path);
    if (clear && c.method === "DELETE") {
      s.organisations = s.organisations.map((o) => (o.account_id === clear[1] ? { ...o, overridden: false } : o));
      return { status: 204, body: {} };
    }
    return undefined;
  });
  return { gw, state: s };
}
