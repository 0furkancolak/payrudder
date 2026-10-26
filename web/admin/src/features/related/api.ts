import { useQuery } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";

/** The invoices of one customer, newest first (the tenant API filters by the customer's external id). */
export function useCustomerInvoices(tenantId: MaybeRefOrGetter<string>, customer: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => ["tenants", toValue(tenantId), "invoices", "of-customer", toValue(customer)] as const),
    queryFn: () => unwrap(tenantApi(toValue(tenantId)).GET("/v1/invoices", { params: { query: { customer: toValue(customer), page_size: PAGE.few } } })),
    enabled: computed(() => toValue(customer) !== ""),
  });
}
