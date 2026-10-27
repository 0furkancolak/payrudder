import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";

/**
 * The integrations of an app that are not payment providers (those are in features/providers/connectApi.ts): the e-invoice integrator and the e-mail provider
 * of the live environment. Nothing here names a tenant or an environment: both are the app's live ones, found by the server from the person's membership.
 * A credential goes up once and is never in an answer.
 */
export const integrationKeys = {
  eInvoice: (appId: string) => ["app", "apps", appId, "integrations", "e-invoice"] as const,
  mail: (appId: string) => ["app", "apps", appId, "integrations", "mail"] as const,
};

/** The integrators a person connects: not the mock, which is the sandbox's. */
export type EInvoiceProvider = "nilvera" | "webhook";
export type EInvoiceMode = "test" | "production";

export function useEInvoiceIntegration(appId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => integrationKeys.eInvoice(toValue(appId))),
    queryFn: async () => (await unwrap(api.GET("/app/v1/apps/{id}/integrations/e-invoice", { params: { path: { id: toValue(appId) } } }))).account ?? null,
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

export function useEInvoiceActions(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  const done = () => queries.invalidateQueries({ queryKey: integrationKeys.eInvoice(toValue(appId)) });
  const path = () => ({ params: { path: { id: toValue(appId) } } });
  return {
    save: useMutation({
      mutationFn: (v: { provider: EInvoiceProvider; mode: EInvoiceMode; credentials: Record<string, string> }) =>
        unwrap(api.PUT("/app/v1/apps/{id}/integrations/e-invoice", { ...path(), body: v })),
      onSuccess: done,
    }),
    remove: useMutation({ mutationFn: () => unwrap(api.DELETE("/app/v1/apps/{id}/integrations/e-invoice", path())), onSuccess: done }),
    test: useMutation({ mutationFn: (taxId: string) => unwrap(api.POST("/app/v1/apps/{id}/integrations/e-invoice/test", { ...path(), body: taxId ? { tax_id: taxId } : {} })) }),
  };
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
