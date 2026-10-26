import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

export type Coupon = components["schemas"]["Coupon"];
export type PromotionCode = components["schemas"]["PromotionCode"];
export type Redemption = components["schemas"]["CouponRedemption"];

export const pricingKeys = {
  coupons: (tenantId: string) => ["tenants", tenantId, "coupons"] as const,
  codes: (tenantId: string) => ["tenants", tenantId, "promotion-codes"] as const,
  redemptions: (tenantId: string) => ["tenants", tenantId, "coupon-redemptions"] as const,
  tax: (tenantId: string) => ["tenants", tenantId, "tax-rates"] as const,
  dunning: (tenantId: string) => ["tenants", tenantId, "dunning-settings"] as const,
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
  /** Extra credits on a credit purchase, in basis points of the credits bought (1000 is 10 % more). */
  bonus_percent_bps?: number;
  applies_to?: CouponTarget[];
  /** Left out when the coupon does not work on subscriptions. */
  duration?: "COUPON_DURATION_ONCE" | "COUPON_DURATION_REPEATING" | "COUPON_DURATION_FOREVER";
  duration_periods?: number;
  max_redemptions?: number;
  /** The coupon stops working at this instant (RFC 3339). */
  expire_time?: string;
  /** Do not make the promotion code with the coupon's own text. */
  without_default_code?: boolean;
}

export type CouponTarget = "COUPON_TARGET_SUBSCRIPTION" | "COUPON_TARGET_CREDIT_PURCHASE" | "COUPON_TARGET_PAYMENT";

/** What every code of one request shares: its limit, expiry and rules. Amounts are minor-unit strings. */
export interface CodeTemplate {
  max_redemptions?: number;
  expire_time?: string;
  first_purchase_only?: boolean;
  one_per_customer?: boolean;
  min_amount_minor?: string;
  min_amount_currency?: string;
  allowed_plans?: string[];
  allowed_packs?: string[];
  allowed_currencies?: string[];
  restore_on_reversal?: boolean;
}

export interface NewCodes {
  coupon: string;
  template: CodeTemplate;
  codes?: string[];
  count?: number;
  prefix?: string;
}

/** The promotion codes, of one coupon or (an empty coupon) of all, a page at a time. */
export function usePromotionCodes(tenantId: MaybeRefOrGetter<string>, coupon: MaybeRefOrGetter<string>, state: MaybeRefOrGetter<string>, search: MaybeRefOrGetter<string>, pageToken: MaybeRefOrGetter<string>, size: MaybeRefOrGetter<number> = 25) {
  return useQuery({
    queryKey: computed(() => [...pricingKeys.codes(toValue(tenantId)), toValue(coupon), toValue(state), toValue(search), toValue(pageToken), toValue(size)]),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/promotion-codes", {
          params: {
            query: {
              ...(toValue(coupon) ? { coupon: toValue(coupon) } : {}),
              page_size: toValue(size),
              ...(toValue(state) ? { state: toValue(state) as "active" } : {}),
              ...(toValue(search) ? { search: toValue(search) } : {}),
              ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}),
            },
          },
        }),
      ),
  });
}

export function useCodeActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => {
    queries.invalidateQueries({ queryKey: pricingKeys.codes(toValue(tenantId)) });
    queries.invalidateQueries({ queryKey: pricingKeys.coupons(toValue(tenantId)) });
  };
  return {
    create: useMutation({
      mutationFn: (v: NewCodes) =>
        unwrap(
          tenantApi(toValue(tenantId)).POST("/v1/coupons/{code}/promotion-codes", {
            params: { path: { code: v.coupon } },
            body: { template: v.template as never, codes: v.codes, count: v.count, prefix: v.prefix },
          }),
        ),
      onSuccess: refresh,
    }),
    deactivate: useMutation({
      mutationFn: (code: string) => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/promotion-codes/{code}", { params: { path: { code } } })),
      onSuccess: refresh,
    }),
  };
}

/** What narrows the redemptions: a coupon, one of its promotion codes, a customer (all optional). */
export interface RedemptionFilter {
  coupon?: string;
  promotionCode?: string;
  customer?: string;
}

export function useRedemptions(tenantId: MaybeRefOrGetter<string>, filter: MaybeRefOrGetter<RedemptionFilter | string>, pageToken: MaybeRefOrGetter<string>, size: MaybeRefOrGetter<number> = 25, enabled: MaybeRefOrGetter<boolean> = true) {
  // A plain string is a coupon code (the form the first pages used).
  const f = () => {
    const v = toValue(filter);
    return typeof v === "string" ? { coupon: v } : v;
  };
  return useQuery({
    queryKey: computed(() => [...pricingKeys.redemptions(toValue(tenantId)), f(), toValue(pageToken), toValue(size)]),
    enabled: computed(() => toValue(enabled)),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/coupon-redemptions", {
          params: {
            query: {
              page_size: toValue(size),
              ...(f().coupon ? { coupon: f().coupon } : {}),
              ...(f().promotionCode ? { promotion_code: f().promotionCode } : {}),
              ...(f().customer ? { customer_external_id: f().customer } : {}),
              ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}),
            },
          },
        }),
      ),
  });
}

/** The codes of the tenant's packages, for a form that restricts a promotion code to some of them. */
export function usePlanCodes(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "plan-codes"] as const),
    queryFn: async () => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/plans"))).plans ?? [],
  });
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

export function useDunningSettings(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => pricingKeys.dunning(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/dunning-settings")),
  });
}

export function useSetDunningSettings(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (retry_hours: number[]) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/dunning-settings", { body: { retry_hours } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: pricingKeys.dunning(toValue(tenantId)) }),
  });
}
