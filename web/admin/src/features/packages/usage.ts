import type { components } from "@/shared/api/schema";
import { parseMajor, parseUnitPrice } from "@/shared/lib/money";
import { isQuantity, quotaTerms, scaleOf, type Entitlement, type Meter, type QuotaDraft } from "@/features/meters/quota";

export type Price = components["schemas"]["Price"];
export type PriceModel = "per_unit" | "tiered" | "package";

/** One usage price as a person types it, in major units of the tenant's currency. */
export interface PriceDraft {
  meter: string;
  model: PriceModel;
  /** Quantity of every billing period that is free; empty = none. */
  included: string;
  unitPrice: string;
  tiers: { upTo: string; unitPrice: string; flatFee: string }[];
  packageSize: string;
  packagePrice: string;
}

export const newPrice = (meter = ""): PriceDraft => ({
  meter,
  model: "per_unit",
  included: "",
  unitPrice: "",
  tiers: [
    { upTo: "", unitPrice: "", flatFee: "" },
    { upTo: "", unitPrice: "", flatFee: "" },
  ],
  packageSize: "",
  packagePrice: "",
});

export interface QuotaRow extends QuotaDraft {
  meter: string;
}

/** What is wrong with a row, for a translated message: the key under billing.packages.usageErrors and its row number and meter. */
export interface UsageError {
  key: "meter" | "duplicate" | "unitPrice" | "included" | "tierBound" | "flatFee" | "packageSize" | "packagePrice" | "limit" | "thresholds" | "softNeedsPrice";
  row: number;
  meter: string;
}

/** The usage prices and the quotas of a package version as the API takes them, or the first thing wrong with them. */
export function buildUsage(prices: readonly PriceDraft[], quotas: readonly QuotaRow[], meters: readonly Meter[], currency: string): { prices: Price[]; entitlements: Entitlement[] } | { error: UsageError } {
  const out: Price[] = [];
  const priced = new Set<string>();
  for (const [i, d] of prices.entries()) {
    const err = (key: UsageError["key"]) => ({ error: { key, row: i + 1, meter: d.meter } });
    if (!meters.some((m) => m.code === d.meter)) return err("meter");
    if (priced.has(d.meter)) return err("duplicate");
    priced.add(d.meter);
    const scale = scaleOf(meters, d.meter);
    const p: Price = { meter_code: d.meter };
    if (d.included.trim() !== "") {
      if (!isQuantity(d.included, scale)) return err("included");
      p.included = d.included.trim();
    }
    if (d.model === "per_unit") {
      const unit = parseUnitPrice(d.unitPrice, currency);
      if (unit === null) return err("unitPrice");
      p.per_unit = { unit_price: unit };
    } else if (d.model === "tiered") {
      const tiers: NonNullable<NonNullable<Price["tiered"]>["tiers"]> = [];
      for (const [j, tier] of d.tiers.entries()) {
        const last = j === d.tiers.length - 1;
        if (!last && !isQuantity(tier.upTo, scale)) return err("tierBound");
        const unit = parseUnitPrice(tier.unitPrice, currency);
        if (unit === null) return err("unitPrice");
        const flat = tier.flatFee.trim() === "" ? "0" : parseMajor(tier.flatFee, currency);
        if (flat === null) return err("flatFee");
        tiers.push({ ...(last ? {} : { up_to: tier.upTo.trim() }), unit_price: unit, ...(flat !== "0" ? { flat_fee_minor: flat } : {}) });
      }
      p.tiered = { tiers };
    } else {
      if (!isQuantity(d.packageSize, scale) || /^0*(\.0*)?$/.test(d.packageSize.trim())) return err("packageSize");
      const price = parseMajor(d.packagePrice, currency);
      if (price === null) return err("packagePrice");
      p.package = { package_size: d.packageSize.trim(), package_price_minor: price };
    }
    out.push(p);
  }
  const entitlements: Entitlement[] = [];
  const limited = new Set<string>();
  for (const [i, q] of quotas.entries()) {
    const err = (key: UsageError["key"]) => ({ error: { key, row: i + 1, meter: q.meter } });
    if (!meters.some((m) => m.code === q.meter)) return err("meter");
    if (limited.has(q.meter)) return err("duplicate");
    limited.add(q.meter);
    // Soft bills what is over the limit at the price: a meter without one would bill nothing (the server refuses it too).
    if (q.mode === "QUOTA_MODE_SOFT" && !priced.has(q.meter)) return err("softNeedsPrice");
    const r = quotaTerms(q, scaleOf(meters, q.meter));
    if ("error" in r) return err(r.error);
    entitlements.push({ meter_code: q.meter, ...r.terms });
  }
  return { prices: out, entitlements };
}
