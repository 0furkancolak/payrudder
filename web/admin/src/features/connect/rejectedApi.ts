import { useInfiniteQuery, useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

export type RejectedEvent = components["schemas"]["RejectedEvent"];

export const rejectedKeys = {
  all: (tenantId: string) => ["tenants", tenantId, "rejected-events"] as const,
  list: (tenantId: string, code: string, meter: string) => ["tenants", tenantId, "rejected-events", "list", code, meter] as const,
  summary: (tenantId: string) => ["tenants", tenantId, "rejected-events", "summary"] as const,
};

/** The events the API refused, newest first, a page at a time (plan 18: the last 14 days). `code` and `meter` are the filters; empty means all. */
export function useRejectedEvents(tenantId: MaybeRefOrGetter<string>, code: MaybeRefOrGetter<string>, meter: MaybeRefOrGetter<string>) {
  return useInfiniteQuery({
    queryKey: computed(() => rejectedKeys.list(toValue(tenantId), toValue(code), toValue(meter))),
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/rejected-events", {
          params: { query: { error_code: toValue(code) || undefined, meter: toValue(meter) || undefined, page_size: 25, page_token: pageParam || undefined } },
        }),
      ),
    getNextPageParam: (last) => last.next_page_token || undefined,
    enabled: computed(() => toValue(tenantId) !== ""),
  });
}

/** The last 24 hours of rejections, by error code and by hour. */
export function useRejectedSummary(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => rejectedKeys.summary(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/rejected-events/summary")),
    enabled: computed(() => toValue(tenantId) !== ""),
  });
}
