import { useInfiniteQuery, useMutation } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, tenantApi, unwrap } from "@/shared/api/client";
import { saveBlob } from "@/shared/lib/download";
import { AUDIT_FILE_NAME, auditQuery, type AuditFilter } from "./lib";

/**
 * Where a log is read from: one tenant's (the tenant API: the person's session plus the tenant as X-Payrudder-Tenant, which is how every tenant page speaks),
 * or the operator's across all tenants (`/admin/v1`, which takes a tenant id as a filter).
 */
export type AuditSource = { kind: "tenant"; tenantId: string } | { kind: "operator" };

const PAGE_SIZE = 50;

export const auditKeys = { all: ["audit"] as const };

/** The log, newest first, a page of 50 at a time with `fetchNextPage` for "Load more". Pages are appended. */
export function useAuditEvents(source: MaybeRefOrGetter<AuditSource>, filter: MaybeRefOrGetter<AuditFilter>) {
  return useInfiniteQuery({
    queryKey: computed(() => [...auditKeys.all, toValue(source), auditQuery(toValue(filter))] as const),
    initialPageParam: "",
    enabled: computed(() => {
      const s = toValue(source);
      return s.kind === "operator" || s.tenantId !== "";
    }),
    queryFn: ({ pageParam }) => {
      const s = toValue(source);
      const { tenant_id, ...query } = auditQuery(toValue(filter));
      const paging = { page_size: PAGE_SIZE, page_token: pageParam || undefined };
      return s.kind === "operator"
        ? unwrap(api.GET("/admin/v1/audit-events", { params: { query: { ...query, ...(tenant_id ? { tenant_id } : {}), ...paging } } }))
        : unwrap(tenantApi(s.tenantId).GET("/v1/audit-events", { params: { query: { ...query, ...paging } } }));
    },
    getNextPageParam: (last) => last.next_page_token || undefined,
  });
}

/**
 * Downloads the log of the filter as a CSV (the newest 5000 entries, the server's own file). The file needs the session token, so it is fetched through
 * the API client and saved from a blob, not linked. A service that cannot be read fails the whole export (503 audit.unavailable) rather than leave entries out.
 */
export function useExportAudit(source: MaybeRefOrGetter<AuditSource>) {
  return useMutation({
    mutationFn: async (filter: AuditFilter) => {
      const s = toValue(source);
      const { tenant_id, ...query } = auditQuery(filter);
      const blob =
        s.kind === "operator"
          ? await unwrap(api.GET("/admin/v1/audit-events/export", { params: { query: { ...query, ...(tenant_id ? { tenant_id } : {}) } }, parseAs: "blob" }))
          : await unwrap(tenantApi(s.tenantId).GET("/v1/audit-events/export", { params: { query }, parseAs: "blob" }));
      // The schema types a text/csv body as a string; with parseAs: "blob" the client hands over the Blob.
      saveBlob(AUDIT_FILE_NAME, blob as unknown as Blob);
    },
  });
}
