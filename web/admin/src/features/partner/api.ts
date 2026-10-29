import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import { personToken } from "@/shared/lib/session";
import type { AffiliationDetail, MyCommission, MyPayout, MyReferral } from "./logic";

// The partner portal API (/app/v1/affiliate/*): session routes of a person, no tenant and no API key. A partner sees only their own data.

type Id = MaybeRefOrGetter<string>;
const enabled = computed(() => personToken.value !== null);

export interface Page<T> {
  items: T[];
  next: string;
  total: number;
}

export const partnerKeys = {
  all: ["app", "affiliate"] as const,
  list: ["app", "affiliate", "affiliations"] as const,
};

export function useAffiliations() {
  return useQuery({ queryKey: partnerKeys.list, queryFn: async () => (await unwrap(api.GET("/app/v1/affiliate/affiliations"))).affiliations ?? [], enabled });
}

/**
 * What a claim that linked records changes: the list of affiliations is read again, and the profile's memberships too (a claim makes the person a member of
 * the seller's account, role affiliate). A claim that linked nothing changes nothing. A function of its own so the rule is tested without a page.
 */
export function invalidateAfterClaim(queries: QueryClient, claimed: number | undefined): void {
  if ((claimed ?? 0) <= 0) return;
  void queries.invalidateQueries({ queryKey: partnerKeys.list });
  void queries.invalidateQueries({ queryKey: ["app", "me"] });
}

/** Links the records a seller made for the person's verified e-mail address to them. Idempotent; the list is fetched again when it claimed something. */
export function useClaim() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST("/app/v1/affiliate/claim")),
    onSuccess: (r) => invalidateAfterClaim(queries, r.claimed),
  });
}

export function useAffiliation(id: Id, from: Id, to: Id, ok: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => [...partnerKeys.all, "detail", toValue(id), toValue(from), toValue(to)]),
    enabled: computed(() => enabled.value && toValue(id) !== "" && toValue(ok)),
    // Changing the period keeps the page (and the picker) while the new figures load.
    placeholderData: keepPreviousData,
    queryFn: (): Promise<AffiliationDetail> =>
      unwrap(api.GET("/app/v1/affiliate/affiliations/{id}", { params: { path: { id: toValue(id) }, query: { from: toValue(from) || undefined, to: toValue(to) || undefined } } })),
  });
}

export function useReferrals(id: Id, pageToken: Id, pageSize: MaybeRefOrGetter<number> = PAGE.paged) {
  return useQuery({
    queryKey: computed(() => [...partnerKeys.all, "referrals", toValue(id), toValue(pageToken), toValue(pageSize)]),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
    queryFn: async (): Promise<Page<MyReferral>> => {
      const r = await unwrap(
        api.GET("/app/v1/affiliate/affiliations/{id}/referrals", { params: { path: { id: toValue(id) }, query: { page_size: toValue(pageSize), ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}) } } }),
      );
      return { items: r.referrals ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

export function useCommissions(id: Id, status: Id, pageToken: Id, pageSize: MaybeRefOrGetter<number> = PAGE.paged) {
  return useQuery({
    queryKey: computed(() => [...partnerKeys.all, "commissions", toValue(id), toValue(status), toValue(pageToken), toValue(pageSize)]),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
    queryFn: async (): Promise<Page<MyCommission>> => {
      const r = await unwrap(
        api.GET("/app/v1/affiliate/affiliations/{id}/commissions", {
          params: { path: { id: toValue(id) }, query: { page_size: toValue(pageSize), ...(toValue(status) ? { status: toValue(status) as "paid" } : {}), ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}) } },
        }),
      );
      return { items: r.commissions ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

export function usePayouts(id: Id, pageToken: Id, pageSize: MaybeRefOrGetter<number> = PAGE.paged) {
  return useQuery({
    queryKey: computed(() => [...partnerKeys.all, "payouts", toValue(id), toValue(pageToken), toValue(pageSize)]),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
    queryFn: async (): Promise<Page<MyPayout>> => {
      const r = await unwrap(
        api.GET("/app/v1/affiliate/affiliations/{id}/payouts", { params: { path: { id: toValue(id) }, query: { page_size: toValue(pageSize), ...(toValue(pageToken) ? { page_token: toValue(pageToken) } : {}) } } }),
      );
      return { items: r.payouts ?? [], next: r.next_page_token ?? "", total: r.total ?? 0 };
    },
  });
}

export function useUpdateAffiliation(id: Id) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: { name?: string; payout_method: "manual" | "bank_transfer"; payout_details?: string }) =>
      unwrap(api.PATCH("/app/v1/affiliate/affiliations/{id}", { params: { path: { id: toValue(id) } }, body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: partnerKeys.all }),
  });
}

/** A seller's program terms for somebody about to apply. 404 affiliate.portal.program_unavailable says nothing about whether the id exists. */
export function useProgramLookup(tenantId: Id) {
  return useQuery({
    queryKey: computed(() => [...partnerKeys.all, "program", toValue(tenantId)]),
    enabled: computed(() => enabled.value && toValue(tenantId) !== ""),
    queryFn: () => unwrap(api.GET("/app/v1/affiliate/programs/{tenant_id}", { params: { path: { tenant_id: toValue(tenantId) } } })),
    retry: false,
  });
}

export function useApply() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: { tenant_id: string; name?: string; payout_method?: "manual" | "bank_transfer"; payout_details?: string }) => unwrap(api.POST("/app/v1/affiliate/applications", { body })),
    onSuccess: () => {
      void queries.invalidateQueries({ queryKey: partnerKeys.all });
      void queries.invalidateQueries({ queryKey: ["app", "me"] });
    },
  });
}
