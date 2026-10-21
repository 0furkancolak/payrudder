// A dashboard answer for the mocked gateway (and for the local mock server used while the UI is built): several currencies, a period and the one before it,
// numbers that move in a believable way but are the same every time. The shape is the gateway's `Dashboard` (see the OpenAPI file).

export interface Params {
  period?: string;
  from?: string;
  to?: string;
  compare?: string;
  environment?: string;
  /** The "today" of the tenant; fixed so a test can name dates. */
  today?: string;
}

const DAY = 86_400_000;
const iso = (d: Date) => d.toISOString().slice(0, 10);
const at = (s: string) => new Date(`${s}T00:00:00Z`);

/** A small deterministic generator so the series look alive and never change between runs. */
function rng(seed: number) {
  let s = seed >>> 0 || 1;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

export function period(p: Params): { from: string; to: string; days: number; gran: "day" | "month"; prevFrom: string; prevTo: string; name: string } {
  const today = at(p.today ?? "2026-10-21");
  const name = p.period ?? "30d";
  let from: Date;
  let to = today;
  if (name === "ytd") from = new Date(Date.UTC(today.getUTCFullYear(), 0, 1));
  else if (name === "custom" && p.from && p.to) {
    from = at(p.from);
    to = at(p.to);
  } else from = new Date(today.getTime() - ((Number.parseInt(name, 10) || 30) - 1) * DAY);
  const days = Math.round((to.getTime() - from.getTime()) / DAY) + 1;
  const prevTo = new Date(from.getTime() - DAY);
  const prevFrom = new Date(prevTo.getTime() - (days - 1) * DAY);
  return { from: iso(from), to: iso(to), days, gran: days > 92 ? "month" : "day", prevFrom: iso(prevFrom), prevTo: iso(prevTo), name };
}

function buckets(from: string, to: string, gran: "day" | "month"): string[] {
  const out: string[] = [];
  if (gran === "month") {
    const f = at(from);
    for (let d = new Date(Date.UTC(f.getUTCFullYear(), f.getUTCMonth(), 1)); d <= at(to); d = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1))) out.push(iso(d));
    return out;
  }
  for (let d = at(from); d <= at(to); d = new Date(d.getTime() + DAY)) out.push(iso(d));
  return out;
}

interface Spec {
  currency: string;
  /** Minor units of net revenue per day, roughly. */
  perDay: number;
  growth: number;
  mrr: number;
  active: number;
  failedRate: number;
  tops: [string, string, number][];
  packages: [string, number, number][];
  aging: [number, number][];
}

const SPECS: Spec[] = [
  { currency: "TRY", perDay: 420_000, growth: 1.18, mrr: 9_400_000, active: 184, failedRate: 720, tops: [["acme", "Acme Ltd", 6_200_000], ["globex", "Globex", 4_100_000], ["initech", "Initech", 2_900_000], ["umbrella", "Umbrella", 1_450_000], ["hooli", "Hooli", 800_000]], packages: [["pro", 120, 7_200_000], ["starter", 64, 2_200_000]], aging: [[7, 2_100_000], [3, 900_000], [1, 310_000], [1, 150_000]] },
  { currency: "EUR", perDay: 31_000, growth: 1.06, mrr: 842_000, active: 42, failedRate: 310, tops: [["hans", "Hans GmbH", 410_000], ["lena", "Lena SARL", 220_000]], packages: [["pro", 30, 690_000], ["starter", 12, 152_000]], aging: [[2, 90_000], [1, 24_000], [0, 0], [0, 0]] },
  { currency: "USD", perDay: 18_500, growth: 0.93, mrr: 488_000, active: 21, failedRate: 1250, tops: [["john", "John Inc", 250_000]], packages: [["pro", 16, 464_000], ["starter", 5, 24_000]], aging: [[1, 29_000], [0, 0], [0, 0], [1, 58_000]] },
  { currency: "JPY", perDay: 9_000, growth: 1.3, mrr: 195_000, active: 5, failedRate: 0, tops: [["yuki", "Yuki KK", 78_000]], packages: [["pro", 5, 195_000]], aging: [[0, 0], [0, 0], [0, 0], [0, 0]] },
  { currency: "KWD", perDay: 4_200, growth: 0.7, mrr: 85_000, active: 1, failedRate: 5000, tops: [], packages: [["pro", 1, 85_000]], aging: [[0, 0], [1, 8_500], [0, 0], [0, 0]] },
];

export function dashboardFixture(p: Params = {}): Record<string, unknown> {
  const per = period(p);
  const compare = p.compare !== "0";
  const cur = buckets(per.from, per.to, per.gran);
  const prev = buckets(per.prevFrom, per.prevTo, per.gran);
  const scale = per.gran === "month" ? 30 : 1;
  const currencies = SPECS.map((s, ci) => {
    const r = rng(1000 + ci);
    const series = cur.map((d, i) => {
      const wave = 0.7 + 0.6 * Math.abs(Math.sin((i + ci) / 3.1)) + 0.2 * r();
      const trend = 0.85 + 0.3 * (i / Math.max(1, cur.length - 1)) * s.growth;
      const value = Math.round(s.perDay * scale * wave * trend);
      const pv = prev[i] === undefined ? undefined : Math.round((s.perDay * scale * (0.7 + 0.6 * Math.abs(Math.sin((i + ci + 4) / 3.1))) * 0.9) / s.growth ** 0.5);
      return { date: d, net_minor: value, ...(compare && pv !== undefined ? { prev_date: prev[i], prev_net_minor: pv } : {}) };
    });
    const net = series.reduce((n, x) => n + x.net_minor, 0);
    const prevNet = compare ? series.reduce((n, x) => n + (x.prev_net_minor ?? 0), 0) : 0;
    const refunds = Math.round(net * 0.03);
    const revenue = (n: number, rf: number) => ({ gross_minor: Math.round(n * 1.2), tax_minor: Math.round(n * 0.17), refunds_minor: rf, net_minor: n, expenses_minor: Math.round(n * 0.22), profit_minor: Math.round(n * 0.78), paid_invoices: Math.max(1, Math.round(n / (s.perDay * 4))) });
    const attempts = Math.max(4, Math.round(s.active * (per.days / 30) * 1.1));
    const failed = Math.round((attempts * s.failedRate) / 10000);
    const payments = (a: number, f: number) => ({ paid: a - f, failed: f, processing: Math.min(3, a - f), refunds: f > 0 ? 1 : 0, paid_minor: net, failed_minor: f * 1500, refunded_minor: refunds, failed_rate_bp: a - f + f > 0 ? Math.round((f * 10000) / a) : null });
    const subs = (m: number, k: number, month: string) => ({ month, active: Math.round(s.active * k), trialing: Math.round(s.active * k * 0.06), paused: 0, past_due: s.failedRate > 1000 ? 1 : 0, mrr_minor: Math.round(m * k), arr_minor: Math.round(m * k * 12), new: Math.round(s.active * 0.08), new_mrr_minor: Math.round(m * 0.07), churned: Math.round(s.active * 0.03 * (2 - k)), churned_mrr_minor: Math.round(m * 0.025), churn_rate_bp: Math.round(300 * (2 - k)) });
    const arpuNow = Math.round(net / Math.max(1, s.active));
    return {
      currency: s.currency,
      revenue: revenue(net, refunds),
      prev_revenue: compare ? revenue(prevNet, Math.round(prevNet * 0.025)) : null,
      series,
      payments: payments(attempts, failed),
      prev_payments: compare ? payments(Math.round(attempts * 0.92), Math.round((attempts * 0.92 * s.failedRate * 0.8) / 10000)) : null,
      subscriptions: subs(s.mrr, 1, per.to.slice(0, 7)),
      prev_subscriptions: compare ? subs(s.mrr, 0.94, per.prevTo.slice(0, 7)) : null,
      arpu_minor: arpuNow,
      prev_arpu_minor: compare ? Math.round(arpuNow * 0.93) : null,
      top_customers: s.tops.map(([id, name, income], i) => ({ customer_id: `cu-${id}`, customer_external_id: id, name, income_minor: Math.round((income * per.days) / 30), invoices: 3 - (i % 2) })),
      top_packages: s.packages.map(([code, active, m]) => ({ plan_code: code, active, mrr_minor: m })),
      aging: (["0-7", "8-30", "31-60", "61+"] as const).map((b, i) => ({ bucket: b, count: s.aging[i]![0], amount_minor: s.aging[i]![1] })).filter((a) => a.count > 0),
      open_count: s.aging.reduce((n, a) => n + a[0], 0),
      open_minor: s.aging.reduce((n, a) => n + a[1], 0),
      renewals: s.active > 1 ? [
        { subscription_id: `sub-${ci}-1`, customer_external_id: s.tops[0]?.[0] ?? "customer", plan_code: s.packages[0]?.[0] ?? "pro", interval: "", renews_at: `2026-10-${23 + ci}T09:00:00Z`, state: "active" },
        { subscription_id: `sub-${ci}-2`, customer_external_id: s.tops[1]?.[0] ?? "other", plan_code: "starter", interval: "year", renews_at: `2026-11-0${1 + (ci % 3)}T09:00:00Z`, state: ci === 2 ? "past_due" : "trialing" },
      ] : [],
    };
  });
  return {
    app_id: "a0000000-0000-4000-8000-000000000001",
    environment: p.environment ?? "live",
    tenant_id: p.environment === "sandbox" ? "a1000000-0000-4000-8000-000000000002" : "a1000000-0000-4000-8000-000000000001",
    timezone: "Europe/Istanbul",
    default_currency: "TRY",
    range: { period: per.name, from: per.from, to: per.to, days: per.days, granularity: per.gran, compare, ...(compare ? { prev_from: per.prevFrom, prev_to: per.prevTo } : {}) },
    generated_at: "2026-10-21T10:00:00Z",
    status: "attention",
    unavailable: [],
    payments_truncated: false,
    payments_prev_covered: true,
    renewals_truncated: false,
    currencies,
    customers: { total: 257, new: per.days >= 90 ? 61 : Math.round(per.days * 0.6), prev_new: compare ? Math.round(per.days * 0.5) : 0 },
    usage: {
      period_id: "2026-10",
      sampled: 10,
      meters: [
        { meter_code: "api_calls", customers: 9, over: 1, near: 2, top: [{ customer_external_id: "globex", used: "1200000", limit: "1000000", percent: 120 }, { customer_external_id: "acme", used: "930000", limit: "1000000", percent: 93 }, { customer_external_id: "initech", used: "812000", limit: "1000000", percent: 81.2 }, { customer_external_id: "hooli", used: "120000", limit: "1000000", percent: 12 }] },
        { meter_code: "storage_gb", customers: 6, over: 0, near: 0, top: [{ customer_external_id: "acme", used: "42", limit: "100", percent: 42 }, { customer_external_id: "umbrella", used: "9", limit: "100", percent: 9 }] },
      ],
    },
    activity: [
      { kind: "invoice", at: "2026-10-21T09:41:00Z", ref: "i1", label: "INV-2026-000412", customer: "acme", status: "paid", amount_minor: 490000, currency: "TRY" },
      { kind: "payment", at: "2026-10-21T09:40:00Z", ref: "p1", label: "charge", status: "paid", amount_minor: 490000, currency: "TRY" },
      { kind: "subscription", at: "2026-10-21T08:12:00Z", ref: "s1", label: "pro", customer: "hans", status: "active", currency: "EUR" },
      { kind: "payment", at: "2026-10-20T17:03:00Z", ref: "p2", label: "charge", status: "failed", amount_minor: 8500, currency: "KWD" },
      { kind: "invoice", at: "2026-10-20T09:00:00Z", ref: "i2", label: "INV-2026-000411", customer: "globex", status: "open", amount_minor: 2990000, currency: "TRY" },
      { kind: "payment", at: "2026-10-19T15:20:00Z", ref: "p3", label: "refund", status: "refunded", amount_minor: 4900, currency: "USD" },
    ],
    health: { payments_reachable: true, webhook_endpoints: 2, webhook_failed: 1, webhook_failed_more: false, providers: [{ provider: "iyzico", environment: "live", is_default: true }, { provider: "paddle", environment: "live", is_default: false }] },
  };
}
