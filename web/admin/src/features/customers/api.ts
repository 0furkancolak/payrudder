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

export interface NewCustomer {
  external_id: string;
  name: string;
  email: string;
  country?: string;
  billing_currency?: string;
}

export function useCreateCustomer(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewCustomer) => unwrap(tenantApi(toValue(tenantId)).POST("/v1/customers", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: customerKeys.list(toValue(tenantId)) }),
  });
}
