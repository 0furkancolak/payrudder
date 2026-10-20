import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

export const pricingKeys = {
  coupons: (tenantId: string) => ["tenants", tenantId, "coupons"] as const,
  tax: (tenantId: string) => ["tenants", tenantId, "tax-rates"] as const,
};

export function useCoupons(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => pricingKeys.coupons(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/coupons")),
  });
}

export interface NewCoupon {
  code: string;
  name: string;
  /** Percentage in basis points (1000 is 10 %), or an amount in minor units of the currency. */
  percent_bps?: number;
  amount_minor?: string;
  currency?: string;
  duration: "COUPON_DURATION_ONCE" | "COUPON_DURATION_REPEATING" | "COUPON_DURATION_FOREVER";
  duration_periods?: number;
  max_redemptions?: number;
}

export function useCouponActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: pricingKeys.coupons(toValue(tenantId)) });
  return {
    create: useMutation({ mutationFn: (body: NewCoupon) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/coupons", { body })), onSuccess: refresh }),
    deactivate: useMutation({
      mutationFn: (code: string) => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/coupons/{code}", { params: { path: { code } } })),
      onSuccess: refresh,
    }),
  };
}

export interface Rate {
  country: string;
  name: string;
  /** What the person typed: a percentage such as 19 or 8.25. */
  percent: string;
}

/** The percentage text of a rate in basis points: 1900 → "19", 825 → "8.25". */
export function percentText(bps: number): string {
  const whole = Math.trunc(bps / 100);
  const frac = bps % 100;
  if (frac === 0) return String(whole);
  return `${whole}.${String(frac).padStart(2, "0").replace(/0$/, "")}`;
}

/** Basis points of a typed percentage, or null if it is not 0–100 with at most two decimals. Done on the text, no float. */
export function parsePercent(input: string): number | null {
  const m = /^(\d{1,3})(?:[.,](\d{1,2}))?$/.exec(input.trim());
  if (!m) return null;
  const bps = Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
  return bps <= 10000 ? bps : null;
}

export function useTaxRates(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => pricingKeys.tax(toValue(tenantId))),
    queryFn: async (): Promise<Rate[]> => {
      const r = await unwrap(tenantApi(toValue(tenantId)).GET("/v1/tax-rates"));
      return (r.rates ?? []).map((x) => ({ country: x.country ?? "", name: x.name ?? "", percent: percentText(x.rate_bps ?? 0) }));
    },
  });
}

export function useSetTaxRates(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (rates: { country: string; name: string; rate_bps: number }[]) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/tax-rates", { body: { rates } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: pricingKeys.tax(toValue(tenantId)) }),
  });
}
