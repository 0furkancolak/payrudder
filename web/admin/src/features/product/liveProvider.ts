/**
 * The forms for connecting a live payment provider yourself. The settings are the JSON payments-worker stores (sealed, never shown again): the same
 * documents the operator pastes on the operator's page, built from fields instead of typed as JSON. `base_url` is never asked: a person gets the provider's own
 * live host, and the server refuses any other.
 */
export type LiveProviderName = "iyzico" | "paddle" | "banktransfer";

/** The providers a person connects themselves, in the order they are offered. */
export const LIVE_PROVIDERS: readonly LiveProviderName[] = ["iyzico", "paddle", "banktransfer"];

export function isLiveProvider(v: unknown): v is LiveProviderName {
  return (LIVE_PROVIDERS as readonly unknown[]).includes(v);
}

export interface ProviderField {
  /** The path in the settings document: "buyer.name" is `{ buyer: { name } }`. Also the key under `product.goLive.provider.fields` for the label. */
  path: string;
  /** A secret is typed as a password, never put back in the form and never shown. */
  secret?: boolean;
  optional?: boolean;
  /** Where the form can prefill it from the billing profile and the person. */
  prefill?: "email" | "legalName" | "taxId" | "line1" | "city" | "country" | "postalCode";
  /** A pattern the value must match (checked after trimming). */
  pattern?: RegExp;
}

const BUYER: ProviderField[] = [
  { path: "buyer.name" },
  { path: "buyer.surname" },
  { path: "buyer.identity_number", prefill: "taxId" },
  { path: "buyer.email", prefill: "email" },
  { path: "buyer.address", prefill: "line1" },
  { path: "buyer.city", prefill: "city" },
  { path: "buyer.country", prefill: "country" },
  { path: "buyer.zip_code", prefill: "postalCode" },
  { path: "buyer.ip", pattern: /^[0-9a-fA-F:.]{3,45}$/ },
  { path: "buyer.gsm_number", optional: true },
];

/** The fields of a provider whose settings are typed fields. Bank transfer has a form of its own (the operator's BankTransferForm). */
export const PROVIDER_FIELDS: Record<"iyzico" | "paddle", readonly ProviderField[]> = {
  iyzico: [{ path: "api_key", secret: true }, { path: "secret_key", secret: true }, ...BUYER],
  paddle: [{ path: "api_key", secret: true }, { path: "webhook_secret", secret: true }, { path: "usage_product_id", pattern: /^pro_[A-Za-z0-9]+$/ }],
};

export type FieldProblem = "required" | "badFormat";
export type ProviderErrors = Record<string, FieldProblem>;

/** Fills the settings document from the values typed (by path). Empty optional fields are left out; nothing else is added. */
export function buildProviderSettings(provider: "iyzico" | "paddle", values: Record<string, string>): { ok: true; settings: Record<string, unknown> } | { ok: false; errors: ProviderErrors } {
  const errors: ProviderErrors = {};
  const settings: Record<string, unknown> = {};
  for (const f of PROVIDER_FIELDS[provider]) {
    const v = (values[f.path] ?? "").trim();
    if (v === "") {
      if (!f.optional) errors[f.path] = "required";
      continue;
    }
    if (f.pattern && !f.pattern.test(v)) {
      errors[f.path] = "badFormat";
      continue;
    }
    const parts = f.path.split(".");
    let node = settings;
    for (const p of parts.slice(0, -1)) node = (node[p] ??= {}) as Record<string, unknown>;
    node[parts[parts.length - 1] as string] = v;
  }
  return Object.keys(errors).length === 0 ? { ok: true, settings } : { ok: false, errors };
}

/** Initial values of the form: what the billing profile and the person already told us, so the buyer details are not typed twice. */
export function prefilled(provider: "iyzico" | "paddle", known: Partial<Record<NonNullable<ProviderField["prefill"]>, string>>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of PROVIDER_FIELDS[provider]) out[f.path] = f.prefill ? (known[f.prefill] ?? "") : "";
  return out;
}

/** Why a live account does not work, as the console words it (`product.goLive.provider.problems.<code>`); anything else reads as the provider refusing. */
export type AccountProblem = "unreadable" | "refused" | "unreachable";
export function problemCode(p: string | undefined): AccountProblem | null {
  if (!p) return null;
  return p === "unreadable" || p === "unreachable" ? p : "refused";
}
