import type { components } from "@/shared/api/schema";
import { thisMonth } from "@/shared/lib/dateRange";

/** The dashboard as the gateway answers it (see `Dashboard` in the OpenAPI file). */
export type Dashboard = components["schemas"]["Dashboard"];
export type DashCurrency = components["schemas"]["DashboardCurrency"];

export const PERIODS = ["7d", "30d", "90d", "ytd", "custom"] as const;
export type Period = (typeof PERIODS)[number];
/** The period of a plain dashboard address. */
export const DEFAULT_PERIOD: Period = "30d";

/** What the page asks for: kept in the address, so a link shares the view and the back button works. */
export interface DashQuery {
  period: Period;
  /** Custom period only: days, YYYY-MM-DD. */
  from?: string;
  to?: string;
  compare: boolean;
  /** The currency tab; empty means the first block. */
  cur?: string;
}

const DAY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Reads the address' search into a query; anything that is not understood falls back to the default: the last 30 days (a named preset, so the
 * period control shows a choice rather than "custom"), compared, first currency.
 */
export function parseQuery(search: Record<string, unknown>, now: Date = new Date()): DashQuery {
  const named = PERIODS.includes(search.period as Period) ? (search.period as Period) : DEFAULT_PERIOD;
  const q: DashQuery = { period: named, compare: !["off", "0", 0, false].includes(search.compare as never) };
  if (named === "custom" && typeof search.from === "string" && typeof search.to === "string" && DAY.test(search.from) && DAY.test(search.to)) {
    q.from = search.from;
    q.to = search.to;
  } else if (named === "custom") {
    const month = thisMonth(now);
    q.from = month.from;
    q.to = month.to;
  }
  if (typeof search.cur === "string" && /^[A-Z]{3}$/.test(search.cur)) q.cur = search.cur;
  return q;
}

/** The query as the search of an address, leaving out what is the default (the last 30 days) so a plain dashboard has a plain address. */
export function toSearch(q: DashQuery): Record<string, string> {
  const s: Record<string, string> = {};
  const isDefault = q.period === DEFAULT_PERIOD;
  if (!isDefault) s.period = q.period;
  if (!isDefault && q.period === "custom" && q.from && q.to) {
    s.from = q.from;
    s.to = q.to;
  }
  if (!q.compare) s.compare = "off";
  if (q.cur) s.cur = q.cur;
  return s;
}

/** The query as the gateway's parameters. */
export function toParams(q: DashQuery): { period: Period; from?: string; to?: string; compare: "0" | "1" } {
  return { period: q.period, ...(q.period === "custom" && q.from && q.to ? { from: q.from, to: q.to } : {}), compare: q.compare ? "1" : "0" };
}

export interface Delta {
  /** Percent change, one decimal; null when there is nothing to compare (the earlier figure was zero or missing). */
  percent: number | null;
  direction: "up" | "down" | "flat" | "new" | "none";
}

/**
 * The change from the earlier figure to the later one. An earlier figure of zero has no percentage: a figure that appears out of nothing is "new", and two
 * zeros have nothing to say (an empty account shows no "unchanged 0%" on every card). Works on whole numbers (minor units, counts), as a float only for the percentage that is shown.
 */
export function delta(now: number | null | undefined, before: number | null | undefined): Delta {
  if (now === null || now === undefined || before === null || before === undefined) return { percent: null, direction: "none" };
  if (before === 0) return now === 0 ? { percent: null, direction: "none" } : { percent: null, direction: "new" };
  const percent = Math.round(((now - before) / Math.abs(before)) * 1000) / 10;
  return { percent, direction: percent > 0 ? "up" : percent < 0 ? "down" : "flat" };
}

/** Basis points as a percentage with up to two decimals (1234 is 12.34), or null. The page rounds it to one decimal when it shows it. */
export function bpPercent(bp: number | null | undefined): number | null {
  return bp === null || bp === undefined ? null : Math.round(bp) / 100;
}

/** The currency blocks in the order the tabs show them, and the one chosen: the query's if it exists, else the first. */
export function chosenCurrency(blocks: readonly DashCurrency[], cur: string | undefined): DashCurrency | undefined {
  return blocks.find((b) => b.currency === cur) ?? blocks[0];
}

/** Whether a block has anything to show: money, payments, subscriptions, invoices open or customers who paid. */
export function hasActivity(b: DashCurrency): boolean {
  return (
    (b.revenue?.gross_minor ?? 0) !== 0 ||
    (b.revenue?.expenses_minor ?? 0) !== 0 ||
    (b.payments?.paid ?? 0) + (b.payments?.failed ?? 0) + (b.payments?.processing ?? 0) + (b.payments?.refunds ?? 0) > 0 ||
    (b.subscriptions?.active ?? 0) + (b.subscriptions?.trialing ?? 0) + (b.subscriptions?.past_due ?? 0) > 0 ||
    (b.open_count ?? 0) > 0
  );
}

// ---- the revenue chart (inline SVG: no chart library)

export interface ChartPoint {
  date: string;
  value: number;
  prevDate?: string;
  prev?: number;
}

export interface Geometry {
  width: number;
  height: number;
  max: number;
  /** The path of the filled area under the line, and of the line itself, in the SVG's own units. */
  area: string;
  line: string;
  prevLine: string;
  /** x of the point i. */
  x: (i: number) => number;
  y: (v: number) => number;
}

/** A "nice" top of the axis at or above the largest value (1, 2, 2.5 or 5 times a power of ten), so the gridlines fall on round numbers. */
export function niceMax(v: number): number {
  if (v <= 0) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = 10 ** exp;
  for (const m of [1, 2, 2.5, 5, 10]) if (m * base >= v) return m * base;
  return 10 * base;
}

/**
 * Works out where the line and the area of a series go in a width x height box with a small padding. One point is drawn as a flat line across the box
 * (there is no slope to show) and an empty series as a flat line at the bottom. Values are drawn as they are: the max is taken over both series.
 */
export function geometry(points: readonly ChartPoint[], width: number, height: number, pad = { l: 4, r: 4, t: 8, b: 4 }): Geometry {
  const n = points.length;
  const max = niceMax(Math.max(0, ...points.map((p) => p.value), ...points.map((p) => p.prev ?? 0)));
  const innerW = width - pad.l - pad.r;
  const innerH = height - pad.t - pad.b;
  const x = (i: number) => (n <= 1 ? pad.l + innerW / 2 : pad.l + (i / (n - 1)) * innerW);
  const y = (v: number) => pad.t + innerH - (v / max) * innerH;
  const trace = (vals: (number | undefined)[]) => {
    const pts = vals.map((v, i) => (v === undefined ? null : ([x(i), y(v)] as const)));
    let d = "";
    pts.forEach((p, i) => {
      if (p) d += `${d === "" || pts[i - 1] === null ? "M" : "L"}${round(p[0])} ${round(p[1])} `;
    });
    return d.trim();
  };
  const line = n === 0 ? "" : n === 1 ? `M${pad.l} ${round(y(points[0]!.value))} L${width - pad.r} ${round(y(points[0]!.value))}` : trace(points.map((p) => p.value));
  const prevLine = points.some((p) => p.prev !== undefined) ? trace(points.map((p) => p.prev)) : "";
  const baseline = pad.t + innerH;
  const area = n < 2 ? "" : `${line} L${round(x(n - 1))} ${baseline} L${round(x(0))} ${baseline} Z`;
  return { width, height, max, area, line, prevLine, x, y };
}

const round = (n: number) => Math.round(n * 10) / 10;

/** The index of the point nearest to an x position in the SVG's units (for the hover). */
export function nearestIndex(g: Geometry, n: number, px: number): number {
  if (n <= 1) return 0;
  let best = 0;
  let bestD = Infinity;
  for (let i = 0; i < n; i++) {
    const d = Math.abs(g.x(i) - px);
    if (d < bestD) {
      best = i;
      bestD = d;
    }
  }
  return best;
}

/** How a bar of a list is sized: the share of the largest, at least a sliver when there is anything. */
export function share(value: number, max: number): number {
  if (max <= 0 || value <= 0) return 0;
  return Math.max(2, Math.min(100, Math.round((value / max) * 100)));
}

/** The sheets open in an address (see shared/lib/sheets): every route that understands its own search keeps them. */
export const sheetSearch = (s: Record<string, unknown>): { sheet?: string; app?: string } => ({
  ...(typeof s.sheet === "string" && s.sheet !== "" ? { sheet: s.sheet } : {}),
  // The scope (an app id or "all") travels in every address (see shared/lib/scope).
  ...(typeof s.app === "string" && s.app !== "" ? { app: s.app } : {}),
});

/**
 * The search of a dashboard address: the environment of an app (sandbox only; live is the default) and the query. Used by the routes, so that a link keeps the
 * view and a value that is not understood is dropped instead of breaking the page.
 */
export function dashSearch(s: Record<string, unknown>): Record<string, string> {
  // Both environments are written out: with none in the address the console chooses by itself (the last one used), and a chosen one must stay chosen.
  return { ...(s.env === "sandbox" || s.env === "live" ? { env: s.env } : {}), ...sheetSearch(s), ...toSearch(parseQuery(s)) };
}

/** What the KPI cards need besides the figures themselves: the rates as percentages, the earlier ones, and every change against the period before. */
export interface KpiFigures {
  rate: number | null;
  prevRate: number | null;
  churn: number | null;
  prevChurn: number | null;
  dNet: Delta | null;
  dMrr: Delta | null;
  dArpu: Delta | null;
  dCustomers: Delta | null;
  dChurn: Delta | null;
  dRate: Delta | null;
  dRefunds: Delta | null;
}

/** The changes of one currency block (and of the customers, which are not per currency) against the period before; all null when the comparison is off. */
export function kpiFigures(block: DashCurrency, customers: Dashboard["customers"] | undefined, compare: boolean): KpiFigures {
  const rev = block.revenue;
  const subs = block.subscriptions;
  const prevRev = compare ? block.prev_revenue : null;
  const prevSubs = compare ? block.prev_subscriptions : null;
  const prevPay = compare ? block.prev_payments : null;
  const rate = bpPercent(block.payments?.failed_rate_bp);
  const prevRate = bpPercent(prevPay?.failed_rate_bp);
  const churn = bpPercent(subs?.churn_rate_bp);
  const prevChurn = bpPercent(prevSubs?.churn_rate_bp);
  const hasArpu = compare && block.prev_arpu_minor !== null && block.prev_arpu_minor !== undefined && block.arpu_minor !== null;
  return {
    rate,
    prevRate,
    churn,
    prevChurn,
    dNet: prevRev ? delta(rev?.net_minor, prevRev.net_minor) : null,
    dMrr: prevSubs ? delta(subs?.mrr_minor, prevSubs.mrr_minor) : null,
    dArpu: hasArpu ? delta(block.arpu_minor, block.prev_arpu_minor) : null,
    dCustomers: compare && customers ? delta(customers.new, customers.prev_new) : null,
    dChurn: prevChurn !== null && churn !== null ? delta(churn, prevChurn) : null,
    dRate: prevRate !== null && rate !== null ? delta(rate, prevRate) : null,
    dRefunds: prevRev ? delta(rev?.refunds_minor, prevRev.refunds_minor) : null,
  };
}

/** The arrow keys, Home and End of a tab list: the index to move to from `index` among `count` tabs, or -1 for a key that moves nothing. */
export function nextTabIndex(key: string, index: number, count: number): number {
  switch (key) {
    case "ArrowRight":
    case "ArrowDown":
      return (index + 1) % count;
    case "ArrowLeft":
    case "ArrowUp":
      return (index - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return -1;
  }
}

const BASIS_POINTS = 10_000;

/** A share in basis points (3 of 12 is 2500), or null when there is nothing to divide by: the failed-payment rate of added-up apps. */
export function ratioBp(part: number, whole: number): number | null {
  return whole > 0 ? Math.round((part * BASIS_POINTS) / whole) : null;
}

/** The currency tabs of several dashboards put together: every currency any of them has, the biggest net revenue first. */
export function mergedCurrencies(dashboards: readonly (Dashboard | undefined)[]): string[] {
  const by = new Map<string, number>();
  for (const d of dashboards) for (const c of d?.currencies ?? []) by.set(c.currency ?? "", (by.get(c.currency ?? "") ?? 0) + (c.revenue?.net_minor ?? 0));
  return [...by.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([c]) => c);
}

/** The revenue series of several blocks added point by point (same period, so the same buckets); the earlier period's figures only where any block has them. */
export function mergedSeries(blocks: readonly (DashCurrency | undefined)[]): ChartPoint[] {
  const length = Math.max(0, ...blocks.map((b) => b?.series?.length ?? 0));
  return Array.from({ length }, (_, i) => {
    let value = 0;
    let prev = 0;
    let hasPrev = false;
    let date = "";
    let prevDate = "";
    for (const b of blocks) {
      const p = b?.series?.[i];
      if (!p) continue;
      date = p.date ?? date;
      value += p.net_minor ?? 0;
      if (p.prev_net_minor !== undefined && p.prev_net_minor !== null) {
        hasPrev = true;
        prev += p.prev_net_minor;
        prevDate = p.prev_date ?? prevDate;
      }
    }
    return { date, value, ...(hasPrev ? { prev, prevDate } : {}) };
  });
}

/** One figure of every block added up (a missing block counts as zero). */
export function sumBlocks(blocks: readonly (DashCurrency | undefined)[], pick: (b: DashCurrency) => number | null | undefined): number {
  return blocks.reduce((n, b) => n + (b ? (pick(b) ?? 0) : 0), 0);
}
