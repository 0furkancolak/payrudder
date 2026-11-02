import { dayStart } from "@/features/audit/lib";
import type { components } from "@/shared/api/schema";
import { API_LOCALES, type ApiLocale } from "@/shared/lib/locale";
import { parseMajor } from "@/shared/lib/money";

export type Kind = components["schemas"]["ContractKind"];
export type Audience = components["schemas"]["ContractAudience"];
export type ContractVersion = components["schemas"]["ContractVersion"];
export type ContractSettings = components["schemas"]["ContractSettings"];
export type ContractAcceptance = components["schemas"]["ContractAcceptance"];
export type ContractWithdrawal = components["schemas"]["ContractWithdrawal"];
export type WithdrawalStatus = ContractWithdrawal["status"];

/** The agreements a seller can present, in the order the API lists them. */
export const KINDS: readonly Kind[] = ["distance_sales", "pre_info", "tos", "privacy"];
export const AUDIENCES: readonly Audience[] = ["consumer", "business"];

/** The languages of a text, each in its own name (a reader finds their language by who speaks it). */
export const LANGUAGE_NAMES: Record<ApiLocale, string> = { en: "English", tr: "Türkçe", de: "Deutsch", fr: "Français", es: "Español", ar: "العربية" };
export const LANGUAGES = API_LOCALES;

/** The only languages with a starter text; the API answers plans.contract.no_starter for the others. */
export const STARTER_LANGUAGES: readonly ApiLocale[] = ["tr", "en"];

/** The placeholders a text may use. The list is fixed by the backend (plans-svc domain.ContractVariables, which a test compares with this one). */
export const VARIABLES = [
  "seller_name",
  "seller_address",
  "seller_tax_id",
  "seller_tax_office",
  "seller_mersis",
  "customer_name",
  "customer_email",
  "customer_address",
  "order_description",
  "order_total",
  "order_currency",
  "date",
] as const;

/** How long the IP address and user agent of an acceptance are kept (months); 36 is the default, from the three years of art. 20 of the Turkish regulation. */
export const RETENTION = { min: 12, max: 120, default: 36 } as const;
export const retentionValid = (n: number): boolean => Number.isInteger(n) && n >= RETENTION.min && n <= RETENTION.max;
/** The renewal notice: days before a renewal, 0 is off (the API's limits, services/plans-svc/internal/domain/contracts.go), and the longest words on how to cancel. */
export const RENEWAL = { min: 0, max: 60, default: 7 } as const;
export const CANCEL_TEXT_MAX = 1000;
export const renewalDaysValid = (n: number): boolean => Number.isInteger(n) && n >= RENEWAL.min && n <= RENEWAL.max;
/** What accepting a withdrawal does about the money: full asks billing for the refund, none leaves it to the seller. */
export const REFUNDS = ["full", "none"] as const;
export type Refund = (typeof REFUNDS)[number];

/** A series is a kind for an audience; its id in the address (`publish-agreement:distance_sales~consumer`). */
export const seriesKey = (kind: Kind, audience: Audience): string => `${kind}~${audience}`;
export function parseSeriesKey(key: string): { kind: Kind; audience: Audience } | undefined {
  const [kind, audience] = key.split("~");
  return (KINDS as readonly string[]).includes(kind ?? "") && (AUDIENCES as readonly string[]).includes(audience ?? "") ? { kind: kind as Kind, audience: audience as Audience } : undefined;
}

/** The newest version of every series that has one, by series key. */
export function currentVersions(versions: readonly ContractVersion[] | undefined): Map<string, ContractVersion> {
  const out = new Map<string, ContractVersion>();
  for (const v of versions ?? []) if (v.current) out.set(seriesKey(v.kind, v.audience), v);
  return out;
}

/** The versions of one series, newest first. */
export const versionsOf = (versions: readonly ContractVersion[] | undefined, kind: Kind, audience: Audience): ContractVersion[] =>
  (versions ?? []).filter((v) => v.kind === kind && v.audience === audience).sort((a, b) => b.number - a.number);

/** The languages a version has a text in, in the console's order. */
export const languagesOf = (v: Pick<ContractVersion, "bodies">): ApiLocale[] => LANGUAGES.filter((l) => (v.bodies[l] ?? "").trim() !== "");
/** The same, each in its own name: "English, Türkçe". */
export const languageNames = (v: Pick<ContractVersion, "bodies">): string => languagesOf(v).map((l) => LANGUAGE_NAMES[l]).join(", ");

/**
 * Whether a text still carries the starter notice, which is its first line. It mirrors NoticeEN and NoticeTR of plans-svc (domain/contracts.go; a test
 * compares them), so the editor can say it before the version is published; the published version's `has_template_notice` is the server's own answer.
 */
export const TEMPLATE_NOTICES = [
  "TEMPLATE - not legal advice. Have a lawyer review and adapt this text before you use it.",
  "ŞABLON - hukuki tavsiye değildir. Bu metni kullanmadan önce bir avukata inceletin ve uyarlayın.",
] as const;
export const hasTemplateNotice = (body: string): boolean => TEMPLATE_NOTICES.some((n) => body.includes(n));

/** Puts `{{name}}` where the cursor is (replacing the selection) and says where the cursor goes next. */
export function insertAtCursor(text: string, start: number, end: number, name: string): { text: string; caret: number } {
  const from = Math.max(0, Math.min(start, text.length));
  const to = Math.max(from, Math.min(end, text.length));
  const token = `{{${name}}}`;
  return { text: text.slice(0, from) + token + text.slice(to), caret: from + token.length };
}

export interface DiffLine {
  op: "same" | "add" | "remove";
  text: string;
}

/** Past this many cells the table of the line diff is not built (about 32 MB): the whole text is shown as replaced. */
const MAX_DIFF_CELLS = 4_000_000;

/**
 * A line diff of two texts: the lines both have stay, the others are removed from `from` or added by `to`. It is the longest common subsequence of the lines
 * (after the shared head and tail are set aside), so a moved paragraph shows as removed and added.
 * shortcut: no word-level marks inside a changed line, and a pair of texts too long for the table are shown as replaced; add Myers' diff if either is missed.
 */
export function diffLines(from: string, to: string): DiffLine[] {
  const a = from === "" ? [] : from.split("\n");
  const b = to === "" ? [] : to.split("\n");
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const ma = a.slice(head, a.length - tail);
  const mb = b.slice(head, b.length - tail);
  const same = (lines: string[]): DiffLine[] => lines.map((text) => ({ op: "same", text }));
  return [...same(a.slice(0, head)), ...middle(ma, mb), ...same(a.slice(a.length - tail))];
}

function middle(a: string[], b: string[]): DiffLine[] {
  const remove = (lines: string[]): DiffLine[] => lines.map((text) => ({ op: "remove", text }));
  const add = (lines: string[]): DiffLine[] => lines.map((text) => ({ op: "add", text }));
  if (a.length === 0 || b.length === 0 || (a.length + 1) * (b.length + 1) > MAX_DIFF_CELLS) return [...remove(a), ...add(b)];
  const w = b.length + 1;
  // lcs[i * w + j] is the length of the common subsequence of a[i..] and b[j..].
  const lcs = new Uint32Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * w + j] = a[i] === b[j] ? (lcs[(i + 1) * w + j + 1] as number) + 1 : Math.max(lcs[(i + 1) * w + j] as number, lcs[i * w + j + 1] as number);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ op: "same", text: a[i] as string });
      i++;
      j++;
    } else if ((lcs[(i + 1) * w + j] as number) >= (lcs[i * w + j + 1] as number)) out.push({ op: "remove", text: a[i++] as string });
    else out.push({ op: "add", text: b[j++] as string });
  }
  return [...out, ...remove(a.slice(i)), ...add(b.slice(j))];
}

/** How many lines a diff removes and adds. */
export const diffStats = (lines: readonly DiffLine[]): { added: number; removed: number } => ({
  added: lines.filter((l) => l.op === "add").length,
  removed: lines.filter((l) => l.op === "remove").length,
});

/**
 * Whether a kind's checkbox in the settings can be changed: a kind with no published consumer version cannot be switched on (the API refuses it with
 * plans.contract.kind_unpublished), and one that is required can always be switched off.
 */
export const requirable = (kind: Kind, settings: Pick<ContractSettings, "required_kinds" | "published_kinds"> | undefined): boolean =>
  (settings?.published_kinds.includes(kind) ?? false) || (settings?.required_kinds.includes(kind) ?? false);

/** The required kinds after one is switched on or off, in the order of KINDS. */
export const withRequired = (required: readonly Kind[], kind: Kind, on: boolean): Kind[] => KINDS.filter((k) => (k === kind ? on : required.includes(k)));

/** What the settings form would send. The API replaces the required kinds and the immediate-start switch as a whole, so all of the form goes together; a cancel text sent empty is removed. */
export interface SettingsDraft {
  required_kinds: Kind[];
  immediate_start: boolean;
  retention_months: number;
  withdrawal_refund: Refund;
  renewal_notice_days: number;
  renewal_cancel_texts: Record<ApiLocale, string>;
  withdrawal_function: boolean;
}
export const settingsDraft = (s: ContractSettings | undefined): SettingsDraft => ({
  required_kinds: KINDS.filter((k) => s?.required_kinds.includes(k)),
  immediate_start: s?.immediate_start ?? false,
  retention_months: s?.retention_months || RETENTION.default,
  withdrawal_refund: s?.withdrawal_refund === "none" ? "none" : "full",
  renewal_notice_days: s?.renewal_notice_days ?? RENEWAL.default,
  renewal_cancel_texts: Object.fromEntries(LANGUAGES.map((l) => [l, s?.renewal_cancel_texts?.[l] ?? ""])) as Record<ApiLocale, string>,
  withdrawal_function: s?.withdrawal_function ?? true,
});
export const settingsChanged = (a: SettingsDraft, b: SettingsDraft): boolean =>
  a.immediate_start !== b.immediate_start ||
  a.retention_months !== b.retention_months ||
  a.required_kinds.join() !== b.required_kinds.join() ||
  a.withdrawal_refund !== b.withdrawal_refund ||
  a.renewal_notice_days !== b.renewal_notice_days ||
  a.withdrawal_function !== b.withdrawal_function ||
  LANGUAGES.some((l) => a.renewal_cancel_texts[l] !== b.renewal_cancel_texts[l]);

/** What the publish confirmation says will happen, as the keys under `contracts.confirm`, in order. */
export function publishFacts(v: { first: boolean; material: boolean; required: boolean; template: boolean }): string[] {
  return ["live", v.first ? "first" : v.material ? "material" : "minor", v.required ? "required" : "notRequired", ...(v.template ? ["template"] : [])];
}

/** The decisions a withdrawal request can take (the API's rule): a received one is decided once, an accepted one is later marked refunded. */
export function withdrawalMoves(status: WithdrawalStatus): Exclude<WithdrawalStatus, "received">[] {
  return status === "received" ? ["accepted", "rejected"] : status === "accepted" ? ["refunded"] : [];
}

/** Whether the seller can ask billing to refund an accepted withdrawal now: it names an invoice and no refund was asked for, or the last request failed. */
export const canRequestRefund = (w: Pick<ContractWithdrawal, "status" | "invoice_id" | "refund_state">): boolean =>
  w.status === "accepted" && w.invoice_id !== "" && (w.refund_state === "none" || w.refund_state === "failed");

/**
 * The refund amount a person typed when accepting a withdrawal or asking again, as the API takes it: empty is "0", the whole of what is left of the invoice. A typed amount
 * must be a positive amount in the invoice's currency, no more than the invoice's total (billing refuses more than what is left of it, which the console cannot know).
 */
export function refundAmount(input: string, currency: string, totalMinor: string): { minor: string } | { error: "amount" | "tooMuch" } {
  if (input.trim() === "") return { minor: "0" };
  const minor = parseMajor(input, currency);
  if (minor === null || minor === "0") return { error: "amount" };
  return BigInt(minor) > BigInt(/^\d+$/.test(totalMinor) ? totalMinor : "0") ? { error: "tooMuch" } : { minor };
}

/** What narrows a customer's acceptance log; empty parts narrow nothing. */
export interface AcceptanceFilter {
  kind: "" | Kind;
  source: "" | "api" | "hosted";
  /** Days as YYYY-MM-DD, both inclusive. */
  range: { from: string; to: string };
}
export const emptyAcceptanceFilter = (): AcceptanceFilter => ({ kind: "", source: "", range: { from: "", to: "" } });
export const isAcceptanceFiltered = (f: AcceptanceFilter): boolean => f.kind !== "" || f.source !== "" || f.range.from !== "" || f.range.to !== "";

/** The query of the filter: the API keeps an acceptance from `from` on and before `to`, so the last day of the range is sent as the start of the day after it. */
export function acceptanceQuery(f: AcceptanceFilter): { kind?: Kind; source?: "api" | "hosted"; from?: string; to?: string } {
  const from = dayStart(f.range.from);
  const to = dayStart(f.range.to, 1);
  return { ...(f.kind !== "" ? { kind: f.kind } : {}), ...(f.source !== "" ? { source: f.source } : {}), ...(from !== "" ? { from } : {}), ...(to !== "" ? { to } : {}) };
}

/** How the go-live checklist sees the agreements: nothing is asked of a seller who requires none, and a required kind that is not ready is a warning, never a block. */
export type Advisory = "optional" | "ready" | "template" | "missing";

export function agreementsAdvisory(settings: Pick<ContractSettings, "required_kinds"> | undefined, versions: readonly ContractVersion[] | undefined): Advisory {
  const required = settings?.required_kinds ?? [];
  if (required.length === 0) return "optional";
  const current = [...currentVersions(versions).values()];
  // A required kind needs a consumer text; a business-only series would leave consumers with nothing to accept.
  if (required.some((k) => !current.some((v) => v.kind === k && v.audience === "consumer"))) return "missing";
  return current.some((v) => required.includes(v.kind) && v.has_template_notice) ? "template" : "ready";
}

/** The kind of a customer's acceptance, grouped by checkout moment. */
export interface AcceptanceGroup {
  id: string;
  time: string;
  source: ContractAcceptance["source"];
  /** The versions accepted together, in the order of KINDS. */
  items: ContractAcceptance[];
  /** One acceptance of the group: the snapshot is fetched through it. */
  anchor: ContractAcceptance;
}

/** A customer's acceptances grouped by `group_id`, newest group first. The evidence shared by a group (time, address, boxes) is the same on each of its rows. */
export function groupAcceptances(list: readonly ContractAcceptance[] | undefined): AcceptanceGroup[] {
  const groups = new Map<string, ContractAcceptance[]>();
  for (const a of list ?? []) groups.set(a.group_id, [...(groups.get(a.group_id) ?? []), a]);
  return [...groups.entries()]
    .map(([id, rows]) => {
      const items = [...rows].sort((a, b) => KINDS.indexOf(a.kind) - KINDS.indexOf(b.kind));
      const anchor = items[0] as ContractAcceptance;
      return { id, time: anchor.accept_time, source: anchor.source, items, anchor };
    })
    .sort((a, b) => Date.parse(b.time) - Date.parse(a.time));
}

/** Who vouches for a piece of evidence: Payrudder saw it, the seller said so, or the API was not told. */
export type Witness = "observed" | "reported";
export const witnessOf = (a: Pick<ContractAcceptance, "observed">, field: "ip" | "user_agent" | "time" | "text"): Witness => (a.observed[field] === true ? "observed" : "reported");

/**
 * The address or browser of an acceptance. A value that is there is shown; a blank one that Payrudder observed was blanked when the retention ran out,
 * and a blank one the seller reported was never given.
 */
export function evidenceValue(a: Pick<ContractAcceptance, "observed" | "ip" | "user_agent">, field: "ip" | "user_agent"): { value: string; state: "present" | "erased" | "none" } {
  const value = a[field];
  if (value !== "") return { value, state: "present" };
  return { value: "", state: a.observed[field] === true ? "erased" : "none" };
}

/**
 * The document a preview is shown in: the server's escaped HTML in a page of its own, in the text's language and direction, with a policy that lets
 * nothing load or run. It goes into an iframe whose sandbox attribute is empty, so it is isolated twice.
 */
export function previewDocument(html: string, locale: string): string {
  const lang = API_LOCALES.includes(locale as ApiLocale) ? locale : "en";
  return `<!doctype html><html lang="${lang}" dir="${lang === "ar" ? "rtl" : "ltr"}"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><style>body{font:14px/1.55 system-ui,sans-serif;color:#1c1c1c;background:#fff;margin:12px}h1,h2,h3{line-height:1.25}mark{background:#fff3b0;color:inherit;border-radius:3px;padding:0 2px}</style></head><body>${html}</body></html>`;
}
