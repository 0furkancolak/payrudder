import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

export const disputeKeys = {
  list: (tenantId: string, invoiceId: string) => ["app", "tenant", tenantId, "disputes", invoiceId] as const,
  history: (tenantId: string, id: string) => ["app", "tenant", tenantId, "disputes", "history", id] as const,
};

/** The cases customers opened with their banks, newest first: the tenant's, or one invoice's when an id is given. Reads GET /v1/disputes. */
export function useDisputes(tenantId: MaybeRefOrGetter<string>, invoiceId: MaybeRefOrGetter<string> = "") {
  return useQuery({
    queryKey: computed(() => disputeKeys.list(toValue(tenantId), toValue(invoiceId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/disputes", { params: { query: { limit: PAGE.whole, invoice_id: toValue(invoiceId) || undefined } } })),
  });
}

/** What was written on a case, newest first: the notes and the marks that it was handled. Reads GET /v1/disputes/{id}/history. */
export function useDisputeHistory(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => disputeKeys.history(toValue(tenantId), toValue(id))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/disputes/{id}/history", { params: { path: { id: toValue(id) } } })),
  });
}

/** A note on a case, and the seller's mark that it was handled. Both refresh every dispute read of the tenant (the cases, their counts and the histories share a key prefix). */
export function useDisputeActions(tenantId: MaybeRefOrGetter<string>, id: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: ["app", "tenant", toValue(tenantId), "disputes"] });
  const path = () => ({ params: { path: { id: toValue(id) } } });
  return {
    addNote: useMutation({ mutationFn: (note: string) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/disputes/{id}/notes", { ...path(), body: { note } })), onSuccess: refresh }),
    setHandled: useMutation({ mutationFn: (handled: boolean) => unwrap(tenantApi(toValue(tenantId)).PUT("/v1/disputes/{id}/handled", { ...path(), body: { handled } })), onSuccess: refresh }),
  };
}
