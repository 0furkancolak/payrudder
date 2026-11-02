import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";

/**
 * The integrations of an app that are not payment providers (those are in features/providers/connectApi.ts): the e-invoice integrator and the e-mail provider
 * of the live environment. Nothing here names a tenant or an environment: both are the app's live ones, found by the server from the person's membership.
 * A credential goes up once and is never in an answer.
 */
export const integrationKeys = {
  eInvoice: (appId: string) => ["app", "apps", appId, "integrations", "e-invoice"] as const,
  eInvoiceRouting: (appId: string) => ["app", "apps", appId, "integrations", "e-invoice", "routing"] as const,
  mail: (appId: string) => ["app", "apps", appId, "integrations", "mail"] as const,
  audit: (appId: string, kind: string) => ["app", "apps", appId, "integrations", "audit", kind] as const,
};

/** The integrators a person connects: not the mock, which is the sandbox's. */
export type EInvoiceProvider = "nilvera" | "webhook";
export type EInvoiceMode = "test" | "production";

/** The e-invoice accounts of the live environment (`accounts`) and the default among them (`account`). */
export function useEInvoiceIntegration(appId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => integrationKeys.eInvoice(toValue(appId))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/integrations/e-invoice", { params: { path: { id: toValue(appId) } } })),
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

/** Account keys name an account: the integrator alone ("nilvera") or with a label ("nilvera~tr"). Empty names "the only one". */
export function useEInvoiceActions(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => Promise.all([queries.invalidateQueries({ queryKey: integrationKeys.eInvoice(toValue(appId)) }), queries.invalidateQueries({ queryKey: integrationKeys.audit(toValue(appId), "") })]);
  const path = () => ({ params: { path: { id: toValue(appId) } } });
  return {
    save: useMutation({
      mutationFn: (v: { provider: EInvoiceProvider; label?: string; makeDefault?: boolean; mode: EInvoiceMode; credentials: Record<string, string> }) =>
        unwrap(api.PUT("/app/v1/apps/{id}/integrations/e-invoice", { ...path(), body: { provider: v.provider, mode: v.mode, credentials: v.credentials, ...(v.label ? { label: v.label } : {}), ...(v.makeDefault ? { default: true } : {}) } })),
      onSuccess: done,
    }),
    remove: useMutation({
      mutationFn: (account: string) => unwrap(api.DELETE("/app/v1/apps/{id}/integrations/e-invoice", { params: { path: { id: toValue(appId) }, query: account ? { account } : {} } })),
      onSuccess: done,
    }),
    test: useMutation({ mutationFn: (v: { account: string; taxId: string }) => unwrap(api.POST("/app/v1/apps/{id}/integrations/e-invoice/test", { ...path(), body: { ...(v.account ? { account: v.account } : {}), ...(v.taxId ? { tax_id: v.taxId } : {}) } })) }),
    setDefault: useMutation({ mutationFn: (account: string) => unwrap(api.POST("/app/v1/apps/{id}/integrations/e-invoice/default", { ...path(), body: { account } })), onSuccess: done }),
  };
}

export function useEInvoiceRouting(appId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => integrationKeys.eInvoiceRouting(toValue(appId))),
    queryFn: async () => (await unwrap(api.GET("/app/v1/apps/{id}/integrations/e-invoice/routing", { params: { path: { id: toValue(appId) } } }))).rules ?? [],
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

export function useSetEInvoiceRouting(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (rules: readonly { country: string; account: string }[]) => unwrap(api.PUT("/app/v1/apps/{id}/integrations/e-invoice/routing", { params: { path: { id: toValue(appId) } }, body: { rules: [...rules] } })),
    onSuccess: () => Promise.all([queries.invalidateQueries({ queryKey: integrationKeys.eInvoiceRouting(toValue(appId)) }), queries.invalidateQueries({ queryKey: integrationKeys.audit(toValue(appId), "") })]),
  });
}

/** Which live payment account would take a customer in a country paying in a currency (the server's own matching, which also knows what each provider can charge). */
export function usePreviewLiveRouting(appId: MaybeRefOrGetter<string>) {
  return useMutation({
    mutationFn: (v: { country: string; currency: string }) => unwrap(api.POST("/app/v1/apps/{id}/go-live/routing/preview", { params: { path: { id: toValue(appId) } }, body: v })),
  });
}

/** The audit trail of the live payment and e-invoice accounts, newest first, a page at a time. `kind` is payment, einvoice or empty for both. */
export function useIntegrationAudit(appId: MaybeRefOrGetter<string>, kind: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useInfiniteQuery({
    queryKey: computed(() => integrationKeys.audit(toValue(appId), toValue(kind))),
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET("/app/v1/apps/{id}/integrations/audit", {
          params: { path: { id: toValue(appId) }, query: { page_size: 20, page_token: pageParam || undefined, kind: (toValue(kind) || undefined) as "payment" | "einvoice" | undefined } },
        }),
      ),
    getNextPageParam: (last) => last.next_page_token || undefined,
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

export function useMailIntegration(appId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => integrationKeys.mail(toValue(appId))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/integrations/mail", { params: { path: { id: toValue(appId) } } })),
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

export function useMailActions(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: integrationKeys.mail(toValue(appId)) });
  const path = () => ({ params: { path: { id: toValue(appId) } } });
  return {
    save: useMutation({
      mutationFn: (v: { baseUrl: string; apiKey: string; from: string }) =>
        unwrap(api.PUT("/app/v1/apps/{id}/integrations/mail", { ...path(), body: { provider: "nafru", base_url: v.baseUrl, api_key: v.apiKey, from: v.from } })),
      onSuccess: done,
    }),
    remove: useMutation({ mutationFn: () => unwrap(api.DELETE("/app/v1/apps/{id}/integrations/mail", path())), onSuccess: done }),
    // A test records what it found (the status shows it), so the connection is read again after it.
    test: useMutation({ mutationFn: (to: string) => unwrap(api.POST("/app/v1/apps/{id}/integrations/mail/test", { ...path(), body: { to } })), onSettled: done }),
  };
}
