import { useMutation, useQueryClient } from "@tanstack/vue-query";
import { toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import type { Address } from "@/shared/lib/address";

/** Saves the seller address printed on the tenant's invoices (operator route PATCH /admin/v1/tenants/{id}). */
export function useSetSellerAddress(tenantId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (address: Address) => unwrap(api.PATCH("/admin/v1/tenants/{id}", { params: { path: { id: toValue(tenantId) } }, body: { address } })),
    // The tenant and the tenant list both carry the address.
    onSuccess: () => queries.invalidateQueries({ queryKey: ["tenants"] }),
  });
}
