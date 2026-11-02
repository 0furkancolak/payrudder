import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";

export type BlocklistEntry = components["schemas"]["BlocklistEntry"];
export const KINDS = ["email", "tax_id", "external_id"] as const;
export type BlocklistKind = (typeof KINDS)[number];

export const blocklistKeys = {
  app: (tenantId: string) => ["tenants", tenantId, "blocklist"] as const,
  platform: ["blocklist", "platform"] as const,
};

export interface NewEntry {
  kind: BlocklistKind;
  value: string;
  note: string;
}

/** The app's blocklist (the tenant API): at most 1000 entries, so one read holds all of it. */
export function useBlocklist(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => blocklistKeys.app(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/blocklist")),
    enabled: computed(() => toValue(tenantId) !== ""),
  });
}

export function useAddBlocklistEntry(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (e: NewEntry) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/blocklist", { body: { kind: e.kind, value: e.value, note: e.note || undefined } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: blocklistKeys.app(toValue(tenantId)) }),
  });
}

export function useRemoveBlocklistEntry(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(tenantApi(toValue(tenantId)).DELETE("/v1/blocklist/{id}", { params: { path: { id } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: blocklistKeys.app(toValue(tenantId)) }),
  });
}

/** The operator's platform-wide list (the admin API). */
export function usePlatformBlocklist() {
  return useQuery({ queryKey: blocklistKeys.platform, queryFn: () => unwrap(api.GET("/admin/v1/blocklist")) });
}

export function useAddPlatformBlocklistEntry() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (e: NewEntry) => unwrap(api.POST("/admin/v1/blocklist", { body: { kind: e.kind, value: e.value, note: e.note || undefined } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: blocklistKeys.platform }),
  });
}

export function useRemovePlatformBlocklistEntry() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/admin/v1/blocklist/{id}", { params: { path: { id } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: blocklistKeys.platform }),
  });
}
