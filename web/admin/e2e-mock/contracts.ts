import type { MockGateway } from "./gateway";

// A stand-in for the contracts API (docs/19-contracts.md): versions that only grow, settings, a preview that finds unknown variables the way the backend does,
// the starter texts, withdrawals and a customer's acceptances. `state` is the test's to read and change while it runs.

export const VARIABLES = ["seller_name", "seller_address", "seller_tax_id", "seller_tax_office", "seller_mersis", "customer_name", "customer_email", "customer_address", "order_description", "order_total", "order_currency", "date"];
const KINDS = ["distance_sales", "pre_info", "tos", "privacy"];

/** The starter text of the mock: a notice on top, a heading and two variables. */
export const STARTER_EN = "**TEMPLATE - not legal advice. Have a lawyer review and adapt this text before you use it.**\n\n# Terms of service\n\nSeller: {{seller_name}}\n\nBuyer: {{customer_name}}";
export const STARTER_TR = "**ŞABLON - hukuki tavsiye değildir. Bu metni kullanmadan önce bir avukata inceletin ve uyarlayın.**\n\n# Kullanım koşulları";
const NOTICES = ["TEMPLATE - not legal advice.", "ŞABLON - hukuki tavsiye değildir."];

export interface Version {
  id: string;
  kind: string;
  audience: string;
  number: number;
  material: boolean;
  bodies: Record<string, string>;
  sha256: string;
  note: string;
  author: string;
  create_time: string;
  variables: string[];
  current: boolean;
  has_template_notice: boolean;
}

export function version(kind: string, audience: string, number: number, bodies: Record<string, string>, extra: Partial<Version> = {}): Version {
  return {
    id: `ver-${kind}-${audience}-${number}`,
    kind,
    audience,
    number,
    material: number === 1,
    bodies,
    sha256: `sha-${kind}-${audience}-${number}`,
    note: "",
    author: "Ada Lovelace",
    create_time: `2026-10-${String(number + 1).padStart(2, "0")}T10:00:00Z`,
    variables: [],
    current: false,
    has_template_notice: Object.values(bodies).some((b) => NOTICES.some((n) => b.includes(n))),
    ...extra,
  };
}

export interface Withdrawal {
  id: string;
  customer_id: string;
  customer_external_id: string;
  subscription_id: string;
  invoice_id: string;
  statement: string;
  source: string;
  status: string;
  receive_time: string;
  decide_time: string | null;
  decided_by: string;
  decision_note: string;
  credit_note_id: string;
  refund_state: "none" | "requesting" | "requested" | "outside" | "failed";
  refund_note: string;
  refund_time: string | null;
}

export const withdrawal = (id: string, customer: string, status: string, statement: string, extra: Partial<Withdrawal> = {}): Withdrawal => ({
  id,
  customer_id: `uuid-${customer}`,
  customer_external_id: customer,
  subscription_id: "",
  invoice_id: "",
  statement,
  source: "hosted",
  status,
  receive_time: "2026-10-18T09:00:00Z",
  decide_time: null,
  decided_by: "",
  decision_note: "",
  credit_note_id: "",
  refund_state: "none",
  refund_note: "",
  refund_time: null,
  ...extra,
});

export interface Acceptance {
  id: string;
  group_id: string;
  customer_id: string;
  version_id: string;
  kind: string;
  audience: string;
  version_number: number;
  version_sha256: string;
  locale_requested: string;
  locale_served: string;
  body_sha256: string;
  context_kind: string;
  context_ref: string;
  source: string;
  observed: Record<string, boolean>;
  checkbox_text: string;
  immediate_start_consent: boolean;
  immediate_start_text: string;
  withdrawal_waiver_acknowledged: boolean;
  withdrawal_waiver_text: string;
  variables: Record<string, string>;
  snapshot_sha256: string;
  ip: string;
  user_agent: string;
  accept_time: string;
  customer_external_id: string;
}

export const acceptance = (id: string, group: string, kind: string, at: string, extra: Partial<Acceptance> = {}): Acceptance => ({
  id,
  group_id: group,
  customer_id: "uuid-cus_1",
  version_id: `ver-${kind}-consumer-1`,
  kind,
  audience: "consumer",
  version_number: 1,
  version_sha256: "vh",
  locale_requested: "tr",
  locale_served: "tr",
  body_sha256: "bh",
  context_kind: "hosted_invoice",
  context_ref: "inv_1",
  source: "hosted",
  observed: { ip: true, user_agent: true, time: true, text: true },
  checkbox_text: "I have read and accept",
  immediate_start_consent: false,
  immediate_start_text: "",
  withdrawal_waiver_acknowledged: false,
  withdrawal_waiver_text: "",
  variables: {},
  snapshot_sha256: `snap-${group}`,
  ip: "203.0.113.7",
  user_agent: "Mozilla/5.0 (X11; Linux)",
  accept_time: at,
  customer_external_id: "cus_1",
  ...extra,
});

/** The page a customer accepted, as the backend stores it. Its script must never run in the console. */
export const SNAPSHOT_HTML = "<!doctype html><html><head><meta charset=\"utf-8\"><title>snapshot</title></head><body><h1>Accepted page</h1><p>Distance sales contract v2</p><script>window.top.__pwned = 1; window.__pwned = 1;</script></body></html>";

export interface ContractsState {
  versions: Version[];
  settings: { required_kinds: string[]; immediate_start: boolean; retention_months: number; withdrawal_refund: string; renewal_notice_days: number; renewal_cancel_texts: Record<string, string>; withdrawal_function: boolean };
  withdrawals: Withdrawal[];
  acceptances: Acceptance[];
}

const escape = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** What the backend's preview answers: the text escaped, each known variable marked, and the names it does not know. */
export function preview(body: string): { html: string; variables: string[]; unknown_variables: string[] } {
  const used: string[] = [];
  const unknown: string[] = [];
  for (const m of body.matchAll(/\{\{\s*([a-z_]+)\s*\}\}/g)) {
    const name = m[1] as string;
    if (!VARIABLES.includes(name)) {
      if (!unknown.includes(name)) unknown.push(name);
    } else if (!used.includes(name)) used.push(name);
  }
  const html = escape(body).replace(/\{\{\s*([a-z_]+)\s*\}\}/g, (_all, name: string) => (VARIABLES.includes(name) ? `<mark>sample ${name}</mark>` : `{{${name}}}`));
  return { html: `<p>${html}</p>`, variables: used, unknown_variables: unknown };
}

/**
 * Answers the contracts routes from `state`. With `tenant` it answers only the calls made for that tenant (the go-live row asks the live one while the page
 * may talk to its sandbox too).
 */
export function contractsGateway(gw: MockGateway, init: Partial<Omit<ContractsState, "settings">> & { settings?: Partial<ContractsState["settings"]> } = {}, tenant = ""): ContractsState {
  const state: ContractsState = {
    versions: [],
    withdrawals: [],
    acceptances: [],
    ...init,
    settings: { required_kinds: [], immediate_start: false, retention_months: 36, withdrawal_refund: "full", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true, ...init.settings },
  };
  // The newest version of a series is the current one, whatever the fixtures said.
  const flag = () => {
    for (const v of state.versions) v.current = !state.versions.some((o) => o.kind === v.kind && o.audience === v.audience && o.number > v.number);
  };
  flag();
  // What billing answers: an invoice named "inv_transfer" was paid by bank transfer and cannot go back through Payrudder.
  const refundOf = (w: Withdrawal) => {
    if (w.invoice_id === "inv_transfer") Object.assign(w, { refund_state: "outside", refund_note: "paid by bank transfer: refund it outside Payrudder", refund_time: "2026-10-21T12:00:00Z" });
    else Object.assign(w, { refund_state: "requested", credit_note_id: "cn-" + w.id, refund_time: "2026-10-21T12:00:00Z" });
  };
  gw.on((c) => {
    if (tenant !== "" && c.headers["x-payrudder-tenant"] !== tenant) return undefined;
    if (c.path === "/v1/contracts/versions" && c.method === "GET") return { body: { versions: [...state.versions].sort((a, b) => b.number - a.number) } };
    if (c.path === "/v1/contracts/versions" && c.method === "POST") {
      const b = c.body as { kind: string; audience?: string; bodies: Record<string, string>; material?: boolean; note?: string };
      const audience = b.audience ?? "consumer";
      const number = Math.max(0, ...state.versions.filter((v) => v.kind === b.kind && v.audience === audience).map((v) => v.number)) + 1;
      const v = version(b.kind, audience, number, b.bodies, { material: b.material === true || number === 1, note: b.note ?? "", author: "Ada Lovelace", create_time: "2026-10-21T12:00:00Z" });
      state.versions.push(v);
      flag();
      return { status: 201, body: v };
    }
    if (c.path === "/v1/contracts/preview" && c.method === "POST") return { body: preview(String(c.body?.body ?? "")) };
    if (c.path === "/v1/contracts/starter" && c.method === "GET") {
      const locale = c.query.get("locale");
      if (locale === "en") return { body: { body: STARTER_EN, locale } };
      if (locale === "tr") return { body: { body: STARTER_TR, locale } };
      return { status: 404, body: { error: { code: "NOT_FOUND", key: "plans.contract.no_starter", message: "there is a starter text in Turkish and English only" } } };
    }
    if (c.path === "/v1/contracts/settings" && c.method === "GET") {
      const published = KINDS.filter((k) => state.versions.some((v) => v.kind === k && v.audience === "consumer"));
      return { body: { ...state.settings, published_kinds: published, update_time: null } };
    }
    if (c.path === "/v1/contracts/settings" && c.method === "PUT") {
      const b = c.body as Partial<ContractsState["settings"]>;
      // The API keeps what a request does not carry, and removes a text that is sent empty.
      const texts = { ...state.settings.renewal_cancel_texts };
      for (const [l, t] of Object.entries(b.renewal_cancel_texts ?? {})) if (t.trim() === "") delete texts[l];
      else texts[l] = t;
      state.settings = {
        required_kinds: b.required_kinds ?? [],
        immediate_start: b.immediate_start === true,
        retention_months: b.retention_months || state.settings.retention_months,
        withdrawal_refund: b.withdrawal_refund || state.settings.withdrawal_refund,
        renewal_notice_days: b.renewal_notice_days ?? state.settings.renewal_notice_days,
        renewal_cancel_texts: texts,
        withdrawal_function: b.withdrawal_function ?? state.settings.withdrawal_function,
      };
      const published = KINDS.filter((k) => state.versions.some((v) => v.kind === k && v.audience === "consumer"));
      return { body: { ...state.settings, published_kinds: published, update_time: "2026-10-21T12:00:00Z" } };
    }
    if (c.path === "/v1/contract-withdrawals" && c.method === "GET") {
      const who = c.query.get("customer_external_id");
      return { body: { withdrawals: state.withdrawals.filter((w) => !who || w.customer_external_id === who) } };
    }
    const decide = /^\/v1\/contract-withdrawals\/([^/]+)\/status$/.exec(c.path);
    if (decide && c.method === "PUT") {
      const w = state.withdrawals.find((x) => x.id === decide[1]);
      const next = String(c.body?.status);
      const moves: Record<string, string[]> = { received: ["accepted", "rejected"], accepted: ["refunded"] };
      if (!w) return { status: 404, body: { error: { code: "NOT_FOUND", key: "plans.contract.withdrawal_not_found", message: "no such withdrawal" } } };
      if (!(moves[w.status] ?? []).includes(next)) return { status: 412, body: { error: { code: "FAILED_PRECONDITION", key: "plans.contract.withdrawal_decided", message: "decided" } } };
      Object.assign(w, { status: next, decide_time: "2026-10-21T12:00:00Z", decided_by: "Ada Lovelace", decision_note: String(c.body?.note ?? "") });
      // Accepting a withdrawal that names an invoice asks billing for the refund when the settings say full: a card payment is requested, a bank transfer is marked.
      if (next === "accepted" && w.invoice_id !== "" && state.settings.withdrawal_refund === "full") refundOf(w);
      return { body: w };
    }
    const refund = /^\/v1\/contract-withdrawals\/([^/]+)\/refund$/.exec(c.path);
    if (refund && c.method === "POST") {
      const w = state.withdrawals.find((x) => x.id === refund[1]);
      if (!w) return { status: 404, body: { error: { code: "NOT_FOUND", key: "plans.contract.withdrawal_not_found", message: "no such withdrawal" } } };
      if (w.status !== "accepted" || w.invoice_id === "" || !["none", "failed"].includes(w.refund_state))
        return { status: 412, body: { error: { code: "FAILED_PRECONDITION", key: "plans.contract.refund_not_possible", message: "not possible" } } };
      refundOf(w);
      return { body: w };
    }
    const record = /^\/v1\/customers\/([^/]+)\/contract-withdrawals$/.exec(c.path);
    if (record && c.method === "POST") {
      const w = withdrawal(`w-new-${state.withdrawals.length + 1}`, record[1] as string, "received", String(c.body?.statement ?? ""), { source: String(c.body?.source ?? "api"), receive_time: String(c.body?.receive_time ?? "2026-10-21T12:00:00Z") });
      state.withdrawals.unshift(w);
      return { status: 201, body: w };
    }
    const accepted = /^\/v1\/customers\/([^/]+)\/contract-acceptances$/.exec(c.path);
    if (accepted && c.method === "GET") return { body: { acceptances: state.acceptances.filter((a) => a.customer_external_id === accepted[1]) } };
    if (/^\/v1\/contract-acceptances\/[^/]+\/snapshot$/.test(c.path) && c.method === "GET") return { body: { html: SNAPSHOT_HTML, sha256: "cafe".repeat(16), group_id: "g-new" } };
    return undefined;
  });
  return state;
}
