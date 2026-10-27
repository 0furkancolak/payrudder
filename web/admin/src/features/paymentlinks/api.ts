import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import type { CreateLinkBody } from "./lib";

export const linkKeys = {
  list: (tenantId: string, kind: string, status: string) => ["tenants", tenantId, "payment-links", kind, status] as const,
};

export function usePaymentLinks(tenantId: MaybeRefOrGetter<string>, kind: MaybeRefOrGetter<string>, status: MaybeRefOrGetter<string>, pageToken: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...linkKeys.list(toValue(tenantId), toValue(kind), toValue(status)), toValue(pageToken)]),
    queryFn: () =>
      unwrap(
        tenantApi(toValue(tenantId)).GET("/v1/payment-links", {
          params: {
            query: {
              page_size: PAGE.paged,
              ...(toValue(kind) ? { kind: toValue(kind) as "invoice" } : {}),
              ...(toValue(status) ? { status: toValue(status) as "active" } : {}),
              ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}),
            },
          },
        }),
      ),
  });
}

export function usePaymentLinkActions(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const refresh = () => queries.invalidateQueries({ queryKey: ["tenants", toValue(tenantId), "payment-links"] });
  const client = () => tenantApi(toValue(tenantId));
  return {
    create: useMutation({ mutationFn: (body: CreateLinkBody) => unwrap(client().POST("/v1/payment-links", { body })), onSuccess: refresh }),
    revoke: useMutation({ mutationFn: (id: string) => unwrap(client().DELETE("/v1/payment-links/{id}", { params: { path: { id } } })), onSuccess: refresh }),
  };
}

/** Portal links: the page where a customer sees their invoices and payments. */
export function usePortalLinkActions(tenantId: MaybeRefOrGetter<string>) {
  const client = () => tenantApi(toValue(tenantId));
  const path = (external_id: string) => ({ params: { path: { external_id } } });
  return {
    create: useMutation({
      mutationFn: (v: { externalId: string; days: number }) => unwrap(client().POST("/v1/customers/{external_id}/portal-links", { ...path(v.externalId), body: { expires_in_days: v.days } })),
    }),
    revoke: useMutation({ mutationFn: (externalId: string) => unwrap(client().DELETE("/v1/customers/{external_id}/portal-links", path(externalId))) }),
  };
}
