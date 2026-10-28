import { useMutation, useQuery, useQueries, useQueryClient, type QueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import { liveKeys, productKeys, sandboxProviderKey } from "@/features/product/api";
import type { LiveProviderName } from "@/features/product/liveProvider";
import type { AppResult, AppRow } from "./connections";

/**
 * Connecting a payment provider from the console, for one app or for every app of the organisation. The organisation-wide version has no endpoint of its own:
 * it goes through each app's own go-live/providers endpoint, one app at a time, so every app's keys are checked at the provider and recorded for the person
 * exactly as a single connection is. A failure on one app does not stop the others; the result says which worked.
 */

/** The providers of every app of the list (live accounts, no settings), one cached query per app under the key `useLiveProviders` uses. */
export function useAppsProviders(apps: MaybeRefOrGetter<readonly AppRow[]>) {
  const results = useQueries({
    queries: computed(() =>
      toValue(apps).map((a) => ({
        queryKey: [...liveKeys.goLive(a.id), "providers"] as const,
        queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/go-live/providers", { params: { path: { id: a.id } } })),
        enabled: a.hasLive,
      })),
    ),
  });
  return computed(() => toValue(apps).map((app, i) => ({ app, query: results.value[i], accounts: results.value[i]?.data?.accounts })));
}

/** Which environment of the app a connection is for: the live one (any number of apps at once) or the sandbox's test mode (one app, a provider with a test host). */
export type ConnectEnvironment = "live" | "sandbox";

/** The app, its checklist and its provider lists are read again after a change. */
function refresh(queries: QueryClient, ids: readonly string[]): Promise<unknown> {
  return Promise.all([
    ...ids.map((id) => queries.invalidateQueries({ queryKey: productKeys.app(id) })),
    ...ids.map((id) => queries.invalidateQueries({ queryKey: sandboxProviderKey(id) })),
    queries.invalidateQueries({ queryKey: productKeys.apps }),
  ]);
}

async function eachApp(apps: readonly AppRow[], act: (id: string) => Promise<unknown>): Promise<AppResult[]> {
  const out: AppResult[] = [];
  for (const a of apps) {
    try {
      await act(a.id);
      out.push({ appId: a.id, name: a.name, ok: true });
    } catch (error) {
      out.push({ appId: a.id, name: a.name, ok: false, error });
    }
  }
  return out;
}

/** Connects (or replaces) the account at a provider on the given apps: the live one, or with environment "sandbox" the test mode of the sandbox. The settings go up as they are typed and are never read back. */
export function useConnectProvider() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { apps: readonly AppRow[]; provider: LiveProviderName; /** The account key when it is a further account ("paddle~eu"); the provider's own name otherwise. */ key?: string; settings: Record<string, unknown>; environment?: ConnectEnvironment }) =>
      eachApp(v.apps, async (id) => {
        if (v.environment === "sandbox") {
          await unwrap(api.PUT("/app/v1/apps/{id}/sandbox/providers/{provider}", { params: { path: { id, provider: v.provider as "iyzico" | "paddle" } }, body: v.settings }));
        } else {
          await unwrap(api.PUT("/app/v1/apps/{id}/go-live/providers/{provider}", { params: { path: { id, provider: v.key ?? v.provider } }, body: v.settings }));
        }
      }),
    onSettled: (_r, _e, v) => refresh(queries, v.apps.map((a) => a.id)),
  });
}

/** Removes the account at a provider from the given apps (the caller passes only the apps that have one): the live one, or the sandbox's test mode. */
export function useDisconnectProvider() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { apps: readonly AppRow[]; provider: LiveProviderName; key?: string; environment?: ConnectEnvironment }) =>
      eachApp(v.apps, async (id) => {
        if (v.environment === "sandbox") {
          await unwrap(api.DELETE("/app/v1/apps/{id}/sandbox/providers/{provider}", { params: { path: { id, provider: v.provider as "iyzico" | "paddle" } } }));
        } else {
          await unwrap(api.DELETE("/app/v1/apps/{id}/go-live/providers/{provider}", { params: { path: { id, provider: v.key ?? v.provider } } }));
        }
      }),
    onSettled: (_r, _e, v) => refresh(queries, v.apps.map((a) => a.id)),
  });
}

/** Makes one of an app's live accounts (by its key, "paddle~eu") the default. The account's settings are not sent again. */
export function useMakeDefaultAccount(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (key: string) => unwrap(api.POST("/app/v1/apps/{id}/go-live/providers/{provider}/default", { params: { path: { id: toValue(appId), provider: key } } })),
    onSettled: () => refresh(queries, [toValue(appId)]),
  });
}

/** One routing rule: a customer in `country` (empty: any) paying in `currency` (empty: any) is charged through the account `provider` (an account key). */
export interface RoutingRule {
  country: string;
  currency: string;
  provider: string;
}

const routingKey = (appId: string) => [...liveKeys.goLive(appId), "routing"] as const;

export function useLiveRouting(appId: MaybeRefOrGetter<string>, on: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => routingKey(toValue(appId))),
    queryFn: async (): Promise<RoutingRule[]> => {
      const r = await unwrap(api.GET("/app/v1/apps/{id}/go-live/routing", { params: { path: { id: toValue(appId) } } }));
      return (r.rules ?? []).map((x) => ({ country: x.country ?? "", currency: x.currency ?? "", provider: x.provider ?? "" }));
    },
    enabled: computed(() => toValue(appId) !== "" && toValue(on)),
  });
}

export function useSetLiveRouting(appId: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (rules: readonly RoutingRule[]) => unwrap(api.PUT("/app/v1/apps/{id}/go-live/routing", { params: { path: { id: toValue(appId) } }, body: { rules: [...rules] } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: routingKey(toValue(appId)) }),
  });
}
