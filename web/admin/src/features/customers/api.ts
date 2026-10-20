import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";

export const customerKeys = {
  list: (tenantId: string) => ["tenants", tenantId, "customers"] as const,
};

export function useCustomers(tenantId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => customerKeys.list(toValue(tenantId))),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/customers", { params: { query: { page_size: 200 } } })),
  });
}

export interface Address {
  line1?: string;
  line2?: string;
  city?: string;
  region?: string;
  postal_code?: string;
  country?: string;
}

/** The address as one line for a table: "Atatürk Cd. 1, 34000 İstanbul, TR". */
export function addressLine(a?: Address): string {
  if (!a) return "";
  return [a.line1, a.line2, [a.postal_code, a.city].filter(Boolean).join(" "), a.region, a.country].filter(Boolean).join(", ");
}

export interface NewCustomer {
  external_id: string;
  name: string;
  email: string;
  country?: string;
  billing_currency?: string;
  address?: Address;
}

export function useCreateCustomer(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewCustomer) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}

/** Replaces a customer's billing address; an empty one clears it. */
export function useSetCustomerAddress(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { externalId: string; address: Address }) =>
      unwrap(tenantApi(toValue(tenantId)).PUT("/v1/customers/{external_id}/address", { params: { path: { external_id: v.externalId } }, body: v.address })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}
