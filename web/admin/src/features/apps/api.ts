import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap, type Tenant } from "@/shared/api/client";
import type { Currency } from "@/shared/lib/currencies";
import { operatorView } from "@/shared/lib/session";

export const appKeys = {
  list: (accountId: string) => ["accounts", accountId, "apps"] as const,
};

/** The apps of an account, each with its sandbox and live tenant. */
export function useApps(accountId: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => [...appKeys.list(toValue(accountId)), operatorView() ? "operator" : "person"]),
    // A person lists their own apps through the product API (the admin API is the operator's); the rows have the same shape.
    queryFn: () => (operatorView() ? unwrap(api.GET("/admin/v1/accounts/{id}/apps", { params: { path: { id: toValue(accountId) } } })) : unwrap(api.GET("/app/v1/apps"))),
    enabled: computed(() => toValue(accountId) !== ""),
  });
}

export interface NewApp {
  name: string;
  slug?: string;
  currency: Currency;
  timezone: string;
  graceHours: number;
}

export function useCreateApp(accountId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: NewApp) =>
      unwrap(
        api.POST("/admin/v1/accounts/{id}/apps", {
          params: { path: { id: toValue(accountId) } },
          body: { name: v.name, slug: v.slug || undefined, currency: v.currency, timezone: v.timezone, grace_period: `${v.graceHours * 3600}s` },
        }),
      ),
    onSuccess: () => queries.invalidateQueries({ queryKey: appKeys.list(toValue(accountId)) }),
  });
}

/** An API key made on this page. Its plaintext is known only now, so the page holds it until the operator dismisses it. */
export interface CreatedKey {
  id: string;
  tenantId: string;
  prefix: string;
  plaintext: string;
  environment: string;
  revoked: boolean;
}

/** Creates a key for a tenant (an app's environment). The plaintext is in the response once and nowhere else. */
export function useCreateApiKey() {
  return useMutation({
    mutationFn: (tenantId: string) => unwrap(api.POST("/admin/v1/tenants/{id}/api-keys", { params: { path: { id: tenantId } } })),
  });
}

export function useRevokeApiKey() {
  return useMutation({
    mutationFn: (keyId: string) => unwrap(api.DELETE("/admin/v1/api-keys/{id}", { params: { path: { id: keyId } } })),
  });
}

/** The environment of a tenant as a lower-case word ("sandbox", "live"). */
export const environmentWord = (t: Tenant): string => (t.environment ?? "").replace("ENVIRONMENT_", "").toLowerCase();

/** The same slug rule as plans-svc: lower-case letters, digits and single dashes, at most 60 characters. Empty means "derive it from the name". */
export const isValidSlug = (slug: string): boolean => slug === "" || (slug.length <= 60 && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(slug));
