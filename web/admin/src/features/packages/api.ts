import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { ApiError, tenantApi, toApiError, unwrap, type Plan, type PlanVersion } from "@/shared/api/client";

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
        if (!(e instanceof ApiError) || toApiError(e).code !== "ALREADY_EXISTS") throw e;
      }
      return unwrap(
        api.POST("/v1/plans/{code}/versions", {
          params: { path: { code: p.code } },
          body: { base_fee_minor: p.baseFeeMinor, fees: p.fees.map((f) => ({ currency: f.currency, base_fee_minor: f.baseFeeMinor, interval: f.interval === "year" ? ("SUBSCRIPTION_INTERVAL_YEAR" as const) : ("SUBSCRIPTION_INTERVAL_MONTH" as const) })) },
        }),
      );
    },
    onSuccess: () => queries.invalidateQueries({ queryKey: packageKeys.list(toValue(tenantId)) }),
  });
}
