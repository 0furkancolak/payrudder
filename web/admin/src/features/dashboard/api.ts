import { keepPreviousData, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { isOperator } from "@/shared/lib/session";
import { toParams, type DashQuery } from "./lib";
import type { Environment } from "@/shared/lib/environment";

/** Where a dashboard is read from: an environment of one of the person's apps, or (for the operator) any tenant. */
export type DashSource = { kind: "app"; appId: string; env: Environment } | { kind: "tenant"; tenantId: string };

export const dashKeys = {
  of: (src: DashSource, q: DashQuery) => ["dashboard", src, { period: q.period, from: q.from, to: q.to, compare: q.compare }] as const,
};

/** One dashboard, read from the gateway: the person's own app, or any tenant for the operator. */
export function fetchDashboard(s: DashSource, query: DashQuery) {
  const q = toParams(query);
  return s.kind === "app"
    ? unwrap(api.GET("/app/v1/apps/{id}/dashboard", { params: { path: { id: s.appId }, query: { environment: s.env, ...q } } }))
    : unwrap(api.GET("/admin/v1/tenants/{tenant_id}/dashboard", { params: { path: { tenant_id: s.tenantId }, query: q } }));
}

/**
 * A dashboard for a period, compared with the one before it. The previous answer stays on the screen while the next one loads (so changing the period
 * does not blank the page) and the numbers are read again once a minute while the page is open (a payment arrives).
 */
export function useDashboard(source: MaybeRefOrGetter<DashSource | null>, query: MaybeRefOrGetter<DashQuery>) {
  return useQuery({
    queryKey: computed(() => {
      const s = toValue(source);
      return s ? dashKeys.of(s, toValue(query)) : (["dashboard", "none"] as const);
    }),
    queryFn: () => {
      const s = toValue(source);
      if (!s) throw new Error("no source");
      return fetchDashboard(s, toValue(query));
    },
    enabled: computed(() => toValue(source) !== null && (toValue(source)?.kind === "tenant" ? isOperator.value : true)),
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  });
}
