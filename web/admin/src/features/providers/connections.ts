import { tenantOf } from "@/shared/lib/environment";
import { problemCode, type AccountProblem, type LiveProviderName } from "@/features/product/liveProvider";

/**
 * The logic behind the "Payment providers" screen: what state a provider is in on an app, how the apps of an organisation add up, and which apps a
 * connection reaches. Pure functions; the page and the sheet only draw their answers.
 */

/** One live account as the app's go-live/providers endpoint reports it (no setting is ever in it). */
export interface AccountView {
  provider?: string;
  default?: boolean;
  working?: boolean;
  checked?: boolean;
  problem?: string;
}

export type ConnectionState = "none" | "working" | "problem";

export interface Connection {
  state: ConnectionState;
  problem: AccountProblem | null;
  isDefault: boolean;
  checked: boolean;
}

/** The state of a provider among an app's live accounts: not connected, connected and accepted by the provider, or connected with a problem. */
export function connectionOf(accounts: readonly AccountView[] | undefined, provider: string): Connection {
  const a = accounts?.find((x) => x.provider === provider);
  if (!a) return { state: "none", problem: null, isDefault: false, checked: false };
  const working = a.working === true;
  return { state: working ? "working" : "problem", problem: working ? null : (problemCode(a.problem) ?? "refused"), isDefault: a.default === true, checked: a.checked === true };
}

/** An app as the screen needs it: its id, name and whether it has a live environment at all. */
export interface AppRow {
  id: string;
  name: string;
  hasLive: boolean;
}

interface AppLike {
  id?: string;
  name?: string;
  tenants?: { id?: string; environment?: string }[];
}

export function appRows(apps: readonly AppLike[] | undefined): AppRow[] {
  return (apps ?? []).map((a) => ({ id: a.id ?? "", name: a.name ?? "", hasLive: tenantOf(a, "live") !== "" })).filter((a) => a.id !== "");
}

export type Scope = "app" | "org";

/**
 * The apps a connect or disconnect reaches: the one app for "this app only", every app with a live environment for the organisation. An organisation-wide
 * connection is made one app at a time through the app's own endpoint, so an app without a live environment cannot take part.
 */
export function targetsOf(scope: Scope, appId: string, apps: readonly AppRow[]): AppRow[] {
  if (scope === "app") return apps.filter((a) => a.id === appId);
  return apps.filter((a) => a.hasLive);
}

/** How an organisation stands on one provider: on how many of its (live-capable) apps it is connected, working, or has a problem. */
export interface OrgSummary {
  apps: number;
  connected: number;
  working: number;
  problems: number;
  state: ConnectionState;
}

export function orgSummary(rows: readonly { app: AppRow; accounts: readonly AccountView[] | undefined }[], provider: string): OrgSummary {
  const live = rows.filter((r) => r.app.hasLive);
  let working = 0;
  let problems = 0;
  for (const r of live) {
    const c = connectionOf(r.accounts, provider);
    if (c.state === "working") working++;
    if (c.state === "problem") problems++;
  }
  const connected = working + problems;
  return { apps: live.length, connected, working, problems, state: problems > 0 ? "problem" : working > 0 ? "working" : "none" };
}

/** The result of acting on one app in an organisation-wide connect: ok, or the error the API gave. */
export interface AppResult {
  appId: string;
  name: string;
  ok: boolean;
  error?: unknown;
}

export function summarize(results: readonly AppResult[]): { ok: number; failed: number; failedApps: string[] } {
  const failed = results.filter((r) => !r.ok);
  return { ok: results.length - failed.length, failed: failed.length, failedApps: failed.map((r) => r.name) };
}

/** Where the keys of a provider are found (its own dashboard), for the help text of the sheet. */
export const PROVIDER_LINKS: Record<LiveProviderName, string | null> = {
  iyzico: "https://merchant.iyzipay.com",
  paddle: "https://vendors.paddle.com/authentication-v2",
  banktransfer: null,
};

/** The i18n key of the help text under a field of a provider's form (`product.providers.help.<provider>.<field>`); fields without help return null. */
const HELPED: Record<"iyzico" | "paddle", readonly string[]> = {
  iyzico: ["api_key", "secret_key", "buyer.identity_number", "buyer.ip"],
  paddle: ["api_key", "webhook_secret", "usage_product_id"],
};
export function helpKey(provider: "iyzico" | "paddle", path: string): string | null {
  return HELPED[provider].includes(path) ? `product.providers.help.${provider}.${path.replace(/\./g, "_")}` : null;
}

/** An account of an app is named by its key: the provider, or the provider, a tilde and a label ("paddle~eu") for a further account at the same provider. */
export function accountKey(provider: string, label: string): string {
  return label === "" ? provider : `${provider}~${label}`;
}

export function splitAccountKey(key: string): { provider: string; label: string } {
  const i = key.indexOf("~");
  return i < 0 ? { provider: key, label: "" } : { provider: key.slice(0, i), label: key.slice(i + 1) };
}

/** What a label may be: lower-case letters, digits and dashes, starting with a letter or digit, at most 31 characters (the server's rule). */
export const ACCOUNT_LABEL = /^[a-z0-9][a-z0-9-]{0,30}$/;

export interface AccountRow {
  key: string;
  provider: string;
  label: string;
  state: ConnectionState;
  problem: AccountProblem | null;
  isDefault: boolean;
}

/** Every account of an app as a row: the primary account of a provider first, then its further accounts by label. */
export function accountRows(accounts: readonly AccountView[] | undefined): AccountRow[] {
  const rows = (accounts ?? [])
    .filter((a) => a.provider)
    .map((a) => {
      const key = a.provider ?? "";
      const c = connectionOf(accounts, key);
      return { key, ...splitAccountKey(key), state: c.state, problem: c.problem, isDefault: c.isDefault };
    });
  return rows.sort((a, b) => a.provider.localeCompare(b.provider) || a.label.localeCompare(b.label));
}
