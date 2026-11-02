import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, tenantApi, unwrap, type Plan, type PlanVersion } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import type { Entitlement } from "@/features/meters/quota";
import type { Price } from "./usage";

export const packageKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "packages"] as const,
};

export interface Package {
  plan: Plan;
  /** The latest published version, absent for a package nothing has been published for yet. */
  version?: PlanVersion;
}

/** The tenant's packages with their latest version. */
export function usePackages(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => packageKeys.list(toValue(tenantId))),
    enabled: computed(() => toValue(tenantId) !== ""),
    queryFn: async (): Promise<Package[]> => {
      const api = tenantApi(toValue(tenantId));
      const { plans = [] } = await unwrap(api.GET("/v1/plans"));
      return Promise.all(
        plans.map(async (plan) => {
          if (!plan.code || !plan.latest_version) return { plan };
          const version = await unwrap(api.GET("/v1/plans/{code}/versions/{version}", { params: { path: { code: plan.code, version: "latest" } } }));
          return { plan, version };
        }),
      );
    },
  });
}

export interface NewPackage {
  code: string;
  name: string;
  /** Fixed fee per month in the tenant's currency, minor units. */
  baseFeeMinor: string;
  /** Fixed fees in other currencies, minor units. */
  fees: { currency: string; interval: "month" | "year"; baseFeeMinor: string }[];
  /** Usage prices in the tenant's currency (minor units), and the quotas of the version. */
  prices?: Price[];
  entitlements?: Entitlement[];
  /** The trial in days a new subscription to this version gets unless it says otherwise (0 or left out: none). */
  trialDays?: number;
  /** Charge the first period after a trial from the trial's end (the server's default for a new version); false charges the whole period. */
  prorateAfterTrial?: boolean;
}

/** Creates the package (unless it exists) and publishes a version with these fixed fees. */
export function useCreatePackage(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (p: NewPackage) => {
      const api = tenantApi(toValue(tenantId));
      try {
        await unwrap(api.POST("/v1/plans", { body: { code: p.code, name: p.name } }));
      } catch (e) {
        // The package exists already: a new version of it is what is wanted.
        if (!(e instanceof ApiError) || e.code !== "ALREADY_EXISTS") throw e;
      }
      return unwrap(
        api.POST("/v1/plans/{code}/versions", {
          params: { path: { code: p.code } },
          body: {
            base_fee_minor: p.baseFeeMinor,
            fees: p.fees.map((f) => ({ currency: f.currency, base_fee_minor: f.baseFeeMinor, interval: f.interval === "year" ? ("SUBSCRIPTION_INTERVAL_YEAR" as const) : ("SUBSCRIPTION_INTERVAL_MONTH" as const) })),
            ...(p.prices?.length ? { prices: p.prices } : {}),
            ...(p.entitlements?.length ? { entitlements: p.entitlements } : {}),
            ...(p.trialDays ? { trial_days: p.trialDays } : {}),
            ...(p.prorateAfterTrial === false ? { prorate_after_trial: false } : {}),
          },
        }),
      );
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: packageKeys.list(toValue(tenantId)) }),
  });
}

export type PriceChangeResult = {
  scheduled: number;
  scheduled_subscription_ids: string[];
  skipped: { subscription_id: string; customer_external_id: string; reason: "renews_soon" | "has_pending_change" | "run_by_provider" | "not_changeable" | "customer_blocked" }[];
  to_version: number;
  notice_days: number;
  /** The price change this run made or continued; empty for a dry run when none is open. */
  price_change_id: string;
  /** Scheduled by that price change before: not scheduled or mailed again. */
  already_scheduled: number;
};

export type PriceChange = components["schemas"]["PriceChange"];
export type PriceChangeItem = components["schemas"]["PriceChangeItem"];

export const priceChangeKeys = {
  list: (tenantId: string, code: string) => ["tenants", tenantId, "price-changes", code] as const,
  one: (tenantId: string, id: string) => ["tenants", tenantId, "price-change", id] as const,
};

/** The price changes made for one package, newest first. */
export function usePriceChanges(tenantId: MaybeRefOrGetter<string>, code: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => priceChangeKeys.list(toValue(tenantId), toValue(code))),
    enabled: computed(() => toValue(enabled) && toValue(tenantId) !== "" && toValue(code) !== ""),
    queryFn: async (): Promise<PriceChange[]> => (await unwrap(tenantApi(toValue(tenantId)).GET("/v1/price-changes", { params: { query: { plan_code: toValue(code) } } }))).price_changes,
  });
}

/** One price change with its subscriptions (at most 500), once it is opened. */
export function usePriceChange(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => priceChangeKeys.one(toValue(tenantId), toValue(id))),
    enabled: computed(() => toValue(tenantId) !== "" && toValue(id) !== ""),
    queryFn: async () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/price-changes/{id}", { params: { path: { id: toValue(id) } } })),
  });
}

/** Tries the skipped subscriptions of a price change again. */
export function useRetryPriceChange(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<PriceChangeResult> => (await unwrap(tenantApi(toValue(tenantId)).POST("/v1/price-changes/{id}/retry", { params: { path: { id } } }))) as PriceChangeResult,
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId)] }),
  });
}

/** Cancels every change of a price change that is still waiting. */
export function useWithdrawPriceChange(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/price-changes/{id}/withdraw", { params: { path: { id } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId)] }),
  });
}

/** Moves the subscriptions on one version of a package to another at their next renewal and mails the customers; `dryRun` only counts. */
export function useSchedulePriceChange(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { code: string; fromVersion: number; toVersion?: number; noticeDays: number; dryRun: boolean }): Promise<PriceChangeResult> =>
      unwrap(
        tenantApi(toValue(tenantId)).POST("/v1/plans/{code}/price-changes", {
          params: { path: { code: v.code } },
          body: { from_version: v.fromVersion, ...(v.toVersion ? { to_version: v.toVersion } : {}), notice_days: v.noticeDays, dry_run: v.dryRun },
        }),
      ) as Promise<PriceChangeResult>,
    onSuccess: (_r, v) => (v.dryRun ? undefined : queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId)] })),
  });
}
