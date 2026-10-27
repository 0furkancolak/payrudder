import { useMutation, useQueries, useQueryClient, type QueryClient } from "@tanstack/vue-query";
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
    mutationFn: (v: { apps: readonly AppRow[]; provider: LiveProviderName; settings: Record<string, unknown>; environment?: ConnectEnvironment }) =>
      eachApp(v.apps, async (id) => {
        if (v.environment === "sandbox") {
          await unwrap(api.PUT("/app/v1/apps/{id}/sandbox/providers/{provider}", { params: { path: { id, provider: v.provider as "iyzico" | "paddle" } }, body: v.settings }));
        } else {
          await unwrap(api.PUT("/app/v1/apps/{id}/go-live/providers/{provider}", { params: { path: { id, provider: v.provider } }, body: v.settings }));
        }
      }),
    onSettled: (_r, _e, v) => refresh(queries, v.apps.map((a) => a.id)),
  });
}

/** Removes the account at a provider from the given apps (the caller passes only the apps that have one): the live one, or the sandbox's test mode. */
export function useDisconnectProvider() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { apps: readonly AppRow[]; provider: LiveProviderName; environment?: ConnectEnvironment }) =>
      eachApp(v.apps, async (id) => {
        if (v.environment === "sandbox") {
          await unwrap(api.DELETE("/app/v1/apps/{id}/sandbox/providers/{provider}", { params: { path: { id, provider: v.provider as "iyzico" | "paddle" } } }));
        } else {
          await unwrap(api.DELETE("/app/v1/apps/{id}/go-live/providers/{provider}", { params: { path: { id, provider: v.provider } } }));
        }
      }),
    onSettled: (_r, _e, v) => refresh(queries, v.apps.map((a) => a.id)),
  });
}
