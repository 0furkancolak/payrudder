import type { AccountProblem } from "@/features/product/liveProvider";
import { accountRows, splitAccountKey, type AccountView } from "@/features/providers/connections";
import { API_LOCALES, type ApiLocale } from "@/shared/lib/locale";
import type { CardState } from "./status";

/**
 * The accounts of an app as one list, whatever they are for: the payment accounts (several at one provider, labelled), the e-invoice accounts (the same) and
 * the e-mail accounts (the same, with Payrudder's built-in sender as the row that is in use while none is connected). Pure functions; the card only draws what
 * they return.
 */
export type AccountKind = "payment" | "einvoice" | "mail";
export const ACCOUNT_KINDS: readonly AccountKind[] = ["payment", "einvoice", "mail"];

export interface AccountEntry {
  /** The test id and the key in a list: the account key, or `mail:builtin` / `mail:nafru` / `mail:nafru~eu` for e-mail. */
  id: string;
  kind: AccountKind;
  /** The account key ("paddle~eu", "nilvera", "nafru~eu"); empty for the built-in sender, which is not an account. */
  key: string;
  provider: string;
  label: string;
  state: CardState;
  problem: AccountProblem | null;
  /** The one a charge, a document or a mail goes to when no rule names another; the built-in sender is "in use" while no e-mail account is connected. */
  isDefault: boolean;
  mode: string;
}

export interface EInvoiceAccountView {
  account?: string;
  provider?: string;
  label?: string;
  mode?: string;
  is_default?: boolean;
}

/** The e-invoice accounts as the API lists them; a gateway that only knows the default (`account`) is read as an environment with that one account. */
export function eInvoiceAccounts(res: { account?: EInvoiceAccountView | null; accounts?: readonly EInvoiceAccountView[] } | null | undefined): EInvoiceAccountView[] {
  if (res?.accounts) return [...res.accounts];
  return res?.account ? [{ ...res.account, account: res.account.account ?? res.account.provider, is_default: res.account.is_default ?? true }] : [];
}

export interface MailAccountView {
  account?: string;
  provider?: string;
  label?: string;
  from?: string;
  base_url?: string;
  key_hint?: string;
  check_problem?: string;
  is_default?: boolean;
}

interface MailView {
  connected?: boolean;
  integration?: MailAccountView | null;
  accounts?: readonly MailAccountView[];
}

/** The own e-mail accounts as the API lists them, the default first; a gateway that only knows the default (`integration`) is read as an app with that one account. */
export function mailAccounts(res: MailView | null | undefined): MailAccountView[] {
  if (res?.accounts) return [...res.accounts];
  return res?.integration ? [{ ...res.integration, account: res.integration.account ?? "nafru", is_default: true }] : [];
}

export function accountEntries(input: { payments: readonly AccountView[] | undefined; eInvoice: readonly EInvoiceAccountView[]; mail: MailView | null | undefined }): AccountEntry[] {
  const out: AccountEntry[] = [];
  for (const r of accountRows(input.payments)) {
    out.push({ id: r.key, kind: "payment", key: r.key, provider: r.provider, label: r.label, state: r.state, problem: r.problem, isDefault: r.isDefault, mode: "" });
  }
  for (const a of input.eInvoice) {
    const key = a.account ?? a.provider ?? "";
    out.push({ id: key, kind: "einvoice", key, provider: a.provider ?? splitAccountKey(key).provider, label: a.label ?? splitAccountKey(key).label, state: "working", problem: null, isDefault: a.is_default === true, mode: a.mode ?? "" });
  }
  const own = mailAccounts(input.mail);
  out.push({ id: "mail:builtin", kind: "mail", key: "", provider: "payrudderMail", label: "", state: own.length > 0 ? "unused" : "always", problem: null, isDefault: own.length === 0, mode: "" });
  for (const a of own) {
    const key = a.account ?? "nafru";
    out.push({ id: `mail:${key}`, kind: "mail", key, provider: a.provider ?? splitAccountKey(key).provider, label: a.label ?? splitAccountKey(key).label, state: a.check_problem ? "problem" : "working", problem: null, isDefault: a.is_default === true, mode: "" });
  }
  return out.sort((a, b) => ACCOUNT_KINDS.indexOf(a.kind) - ACCOUNT_KINDS.indexOf(b.kind) || a.provider.localeCompare(b.provider) || a.label.localeCompare(b.label));
}

export interface EInvoiceRule {
  country: string;
  account: string;
}

export interface Taker {
  /** The account that takes the document, or null when the environment has no default and no rule names the country. */
  account: string | null;
  via: "rule" | "default" | "none";
}

/** Which e-invoice account would issue the document of a buyer in `country`: the account a rule names for it, else the default (the worker's own rule). */
export function eInvoiceTaker(accounts: readonly EInvoiceAccountView[], rules: readonly EInvoiceRule[], country: string): Taker {
  const c = country.trim().toUpperCase();
  const rule = c === "" ? undefined : rules.find((r) => r.country.toUpperCase() === c);
  if (rule && accounts.some((a) => (a.account ?? a.provider) === rule.account)) return { account: rule.account, via: "rule" };
  const def = accounts.find((a) => a.is_default);
  return def ? { account: def.account ?? def.provider ?? null, via: "default" } : { account: null, via: "none" };
}

export interface MailRule {
  language: string;
  account: string;
}

export interface MailTaker {
  /** The own account that sends the mail, or null when Payrudder's built-in sender does (no account is connected). */
  account: string | null;
  via: "rule" | "default" | "builtin";
}

/** Which e-mail account would send a mail in `language`: the account a rule names for it, else the default (the notifier's own rule: no language is English). */
export function mailTaker(accounts: readonly MailAccountView[], rules: readonly MailRule[], language: string): MailTaker {
  if (accounts.length === 0) return { account: null, via: "builtin" };
  const lang = language === "" ? "en" : language;
  const rule = rules.find((r) => r.language === lang);
  if (rule && accounts.some((a) => a.account === rule.account)) return { account: rule.account, via: "rule" };
  const def = accounts.find((a) => a.is_default) ?? accounts[0];
  return { account: def?.account ?? null, via: "default" };
}

/** The languages a mail is in, and so the languages a routing rule can name. */
export const MAIL_LANGUAGES: readonly ApiLocale[] = API_LOCALES;

/** What an audit entry says, as the key of its sentence (`product.integrations.accounts.audit.actions.<action>`). */
export const AUDIT_ACTIONS = ["connected", "replaced", "removed", "default_set", "routing_changed"] as const;

/** Who an audit entry names: a person of the app by name when known, "Payrudder support" for the operator, otherwise a short form of the id. */
export function auditActor(actor: string, names: ReadonlyMap<string, string>, support: string): string {
  if (actor === "operator") return support;
  const id = actor.startsWith("user:") ? actor.slice(5) : actor;
  return names.get(id) ?? (id.length > 8 ? `${id.slice(0, 8)}…` : id);
}

/** How a payment account is doing, for its pill and its filter: what the last periodic check found, unless the circuit breaker says more. */
export const HEALTH_STATES = ["healthy", "down", "bad_credentials", "circuit_open", "recovering", "unknown"] as const;
export type HealthState = (typeof HEALTH_STATES)[number];

export interface HealthView {
  provider?: string;
  status?: string;
  breaker?: string;
  consecutive_failures?: number;
  last_error?: string;
  last_checked_at?: string;
  last_ok_at?: string;
  failover_to?: string;
}

/** An open circuit is the news whatever the last check said (charges are being refused); one letting a probe through is recovering; no row, or a status we do not know, is unknown. */
export function healthState(h: HealthView | undefined): HealthState {
  if (!h) return "unknown";
  if (h.breaker === "open") return "circuit_open";
  if (h.breaker === "half_open") return "recovering";
  return h.status === "healthy" || h.status === "down" || h.status === "bad_credentials" ? h.status : "unknown";
}

export const healthTone = (s: HealthState): "success" | "danger" | "warning" | "neutral" => (s === "healthy" ? "success" : s === "recovering" ? "warning" : s === "unknown" ? "neutral" : "danger");

/** A bank transfer is confirmed by hand: it has no charge to fail over and cannot take one over. */
export const canFailover = (e: Pick<AccountEntry, "kind" | "provider">): boolean => e.kind === "payment" && e.provider !== "banktransfer";

/** The accounts a failover from `key` may go to: the app's other payment accounts that can take part. */
export const failoverTargets = (entries: readonly AccountEntry[], key: string): AccountEntry[] => entries.filter((e) => canFailover(e) && e.key !== key);
