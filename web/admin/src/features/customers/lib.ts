import type { components } from "@/shared/api/schema";

type S = components["schemas"];
export type Customer = S["Customer"];
type Invoice = Pick<S["Invoice"], "id" | "customer_external_id" | "currency" | "total_minor" | "status" | "finalize_time" | "create_time" | "payments">;
type Payment = Pick<S["Payment"], "invoice_id" | "kind" | "status" | "amount_minor" | "currency" | "occurred_at">;
type Subscription = Pick<S["Subscription"], "customer_external_id" | "state" | "plan_code">;

/** The tabs of a customer's sheet. */
export type CustomerTab = "overview" | "payments" | "invoices" | "subscriptions" | "credits" | "details";

/** Amounts by currency, in minor units. A customer may pay in more than one currency; amounts of different currencies are never added together. */
export type Amounts = Map<string, bigint>;

/** The headline state of a customer's subscriptions, the worst first: a past-due one says more than an active one. */
export type SubscriptionStatus = "pastDue" | "active" | "trialing" | "paused" | "cancelled" | "none";

/** What the console can say about a customer from the invoices, payments and subscriptions it has already loaded for the tenant. */
export interface CustomerSummary {
  /** Paid invoices, less refunds. */
  spend: Amounts;
  /** What open invoices still ask for (less payments recorded against them by hand). */
  open: Amounts;
  openInvoices: number;
  uncollectibleInvoices: number;
  invoices: number;
  /** Subscriptions that bill now (active, trialing or past due). */
  activeSubscriptions: number;
  subscriptions: number;
  subscriptionStatus: SubscriptionStatus;
  /** The plans of the subscriptions that bill now, as their codes. */
  plans: string[];
  /** When the customer last paid (a provider charge or a payment recorded by hand), ISO time. */
  lastPayment?: string;
  /** Unpaid invoices given up on, or a subscription past due. */
  delinquent: boolean;
}

const ZERO = BigInt(0);
const toBig = (v: string | number | undefined): bigint => {
  try {
    return v === undefined || v === "" ? ZERO : BigInt(v);
  } catch {
    return ZERO;
  }
};
const add = (m: Amounts, currency: string | undefined, minor: bigint) => {
  if (!currency || minor === ZERO) return;
  m.set(currency, (m.get(currency) ?? ZERO) + minor);
};
const later = (a: string | undefined, b: string | undefined) => (!a ? b : !b ? a : a >= b ? a : b);

export function emptySummary(): CustomerSummary {
  return { spend: new Map(), open: new Map(), openInvoices: 0, uncollectibleInvoices: 0, invoices: 0, activeSubscriptions: 0, subscriptions: 0, subscriptionStatus: "none", plans: [], delinquent: false };
}

const STATUS_RANK: Record<SubscriptionStatus, number> = { pastDue: 5, active: 4, trialing: 3, paused: 2, cancelled: 1, none: 0 };
const STATE_STATUS: Record<string, SubscriptionStatus> = {
  SUBSCRIPTION_STATE_PAST_DUE: "pastDue",
  SUBSCRIPTION_STATE_ACTIVE: "active",
  SUBSCRIPTION_STATE_TRIALING: "trialing",
  SUBSCRIPTION_STATE_PAUSED: "paused",
  SUBSCRIPTION_STATE_CANCELLED: "cancelled",
};

/**
 * One summary per customer external id, from the tenant's invoices, payments and subscriptions as far as they are loaded (the newest page of each). A
 * payment names an invoice, not a customer, so a payment whose invoice is not among the loaded ones is left out.
 */
export function summarise(invoices: readonly Invoice[], payments: readonly Payment[], subscriptions: readonly Subscription[]): Map<string, CustomerSummary> {
  const out = new Map<string, CustomerSummary>();
  const of = (id: string) => {
    let s = out.get(id);
    if (!s) out.set(id, (s = emptySummary()));
    return s;
  };
  const owner = new Map<string, string>();
  for (const inv of invoices) {
    const id = inv.customer_external_id;
    if (!id) continue;
    if (inv.id) owner.set(inv.id, id);
    const s = of(id);
    if (inv.status !== "draft" && inv.status !== "void") s.invoices++;
    const manual = (inv.payments ?? []).reduce((sum, p) => sum + toBig(p.amount_minor), ZERO);
    for (const p of inv.payments ?? []) s.lastPayment = later(s.lastPayment, p.received_at);
    if (inv.status === "paid") add(s.spend, inv.currency, toBig(inv.total_minor));
    if (inv.status === "open") {
      s.openInvoices++;
      const left = toBig(inv.total_minor) - manual;
      add(s.open, inv.currency, left > ZERO ? left : ZERO);
    }
    if (inv.status === "uncollectible") s.uncollectibleInvoices++;
  }
  for (const p of payments) {
    const id = owner.get(p.invoice_id ?? "");
    if (!id) continue;
    const s = of(id);
    if (p.status === "refunded" || (p.kind === "refund" && p.status === "paid")) add(s.spend, p.currency, -toBig(p.amount_minor));
    else if (p.status === "paid" && p.kind !== "refund") s.lastPayment = later(s.lastPayment, p.occurred_at);
  }
  for (const sub of subscriptions) {
    const id = sub.customer_external_id;
    if (!id) continue;
    const s = of(id);
    s.subscriptions++;
    const status = STATE_STATUS[sub.state ?? ""] ?? "none";
    if (status === "active" || status === "trialing" || status === "pastDue") {
      s.activeSubscriptions++;
      if (sub.plan_code && !s.plans.includes(sub.plan_code)) s.plans.push(sub.plan_code);
    }
    if (STATUS_RANK[status] > STATUS_RANK[s.subscriptionStatus]) s.subscriptionStatus = status;
  }
  for (const s of out.values()) s.delinquent = s.uncollectibleInvoices > 0 || s.subscriptionStatus === "pastDue";
  return out;
}

/**
 * The amount to show for a set of amounts in one cell: the customer's billing currency when they have one, else the largest; `more` counts the other
 * currencies. Undefined when there is nothing.
 */
export function headline(amounts: Amounts, prefer?: string): { minor: string; currency: string; more: number } | undefined {
  if (amounts.size === 0) return undefined;
  let pick = prefer && amounts.has(prefer) ? prefer : undefined;
  if (!pick) {
    let best = ZERO;
    for (const [c, v] of amounts) {
      const abs = v < ZERO ? -v : v;
      if (!pick || abs > best) [pick, best] = [c, abs];
    }
  }
  const currency = pick as string;
  return { minor: String(amounts.get(currency) ?? ZERO), currency, more: amounts.size - 1 };
}

/** A country's flag as an emoji from its ISO 3166 alpha-2 code ("TR" → 🇹🇷); empty for anything else. */
export function flagOf(country: string | undefined): string {
  const cc = (country ?? "").trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(cc)) return "";
  return String.fromCodePoint(...[...cc].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

/** The country's name in the console's language ("TR" → "Türkiye"), or the code when the browser does not know it. */
export function countryName(country: string | undefined, locale: string): string {
  const cc = (country ?? "").trim().toUpperCase();
  if (!cc) return "";
  try {
    return new Intl.DisplayNames([locale], { type: "region" }).of(cc) ?? cc;
  } catch {
    return cc;
  }
}

/** One or two letters for a customer's avatar: the first letters of the first two words of the name, else of the e-mail, else of the id. */
export function initialsOf(c: Pick<Customer, "name" | "email" | "external_id">, locale = "en"): string {
  const source = (c.name ?? "").trim() || (c.email ?? "").split("@")[0]?.trim() || (c.external_id ?? "").trim();
  if (!source) return "?";
  const words = source.split(/[\s._-]+/u).filter(Boolean);
  const letters = (words.length > 1 ? [words[0], words[1]] : [source]).map((w) => [...(w ?? "")][0] ?? "");
  return letters.join("").toLocaleUpperCase(locale) || "?";
}
