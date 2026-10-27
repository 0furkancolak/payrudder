import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { personToken } from "@/shared/lib/session";

// Payrudder as a SaaS (phase 55): the plan of the organisation (person) and the organisations with their plans (operator). Types come from the OpenAPI file.

export const saasKeys = {
  plan: ["app", "account", "plan"] as const,
  invoices: ["app", "account", "plan", "invoices"] as const,
  info: ["admin", "saas"] as const,
  organizations: (q: string, token: string) => ["admin", "saas", "organizations", q, token] as const,
};

const enabled = computed(() => personToken.value !== null);

/** The plan of the account: current plan, quotas and the plans on offer. `enabled: false` in the answer means this deployment is not a SaaS. */
export function usePlan() {
  return useQuery({ queryKey: saasKeys.plan, queryFn: () => unwrap(api.GET("/app/v1/account/plan")), enabled, retry: false, staleTime: 30_000 });
}

/** Whether the plan page and its menu entry exist: false until the answer says so. */
export function useSaasEnabled() {
  const plan = usePlan();
  return computed(() => plan.data.value?.enabled === true);
}

/** Moves the account to another plan; the answer is the new state of the plan page. */
export function useChangePlan() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (planCode: string) => unwrap(api.PUT("/app/v1/account/plan", { body: { plan_code: planCode } })),
    onSuccess: (plan) => {
      queries.setQueryData(saasKeys.plan, plan);
      void queries.invalidateQueries({ queryKey: saasKeys.invoices });
    },
  });
}

export function usePlanInvoices(on: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: saasKeys.invoices,
    queryFn: () => unwrap(api.GET("/app/v1/account/plan/invoices")),
    enabled: computed(() => enabled.value && toValue(on)),
  });
}

/** A payment link for an unpaid invoice of the plan. */
export function usePayLink() {
  return useMutation({ mutationFn: (invoiceId: string) => unwrap(api.POST("/app/v1/account/plan/invoices/{id}/pay-link", { params: { path: { id: invoiceId } } })) });
}

/** A link to the customer portal of the plan (payment method, invoices). */
export function usePortalLink() {
  return useMutation({ mutationFn: () => unwrap(api.POST("/app/v1/account/plan/portal-link")) });
}

/** Whether SaaS runs here and which tenant owns the plans (operator). */
export function useSaasInfo(on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({ queryKey: saasKeys.info, queryFn: () => unwrap(api.GET("/admin/v1/saas")), enabled: computed(() => enabled.value && toValue(on)), retry: false });
}

/** One page of organisations, found by name or owner address. */
export function useOrganizations(q: MaybeRefOrGetter<string>, pageToken: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => saasKeys.organizations(toValue(q), toValue(pageToken))),
    queryFn: () => unwrap(api.GET("/admin/v1/organizations", { params: { query: { page_size: 25, q: toValue(q) || undefined, page_token: toValue(pageToken) || undefined } } })),
    enabled,
    placeholderData: keepPreviousData,
  });
}

/** The operator's manual override: a plan without a checkout and/or limits of the organisation's own. */
export function useSetOrganizationPlan() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { accountId: string; plan_code?: string; limits?: Record<string, number> }) =>
      unwrap(api.PUT("/admin/v1/organizations/{account_id}/plan", { params: { path: { account_id: v.accountId } }, body: { plan_code: v.plan_code, limits: v.limits } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["admin", "saas", "organizations"] }),
  });
}

export function useClearOverrides() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (accountId: string) => unwrap(api.DELETE("/admin/v1/organizations/{account_id}/overrides", { params: { path: { account_id: accountId } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: ["admin", "saas", "organizations"] }),
  });
}
