import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { api, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import type { Currency } from "@/shared/lib/currencies";
import { personToken, setPersonToken } from "@/shared/lib/session";

// The product API (/auth, /app/v1): what a signed-in person sees in their accounts. Types come from the OpenAPI file.

type S = components["schemas"];
export type SessionOpened = S["SessionOpened"];
export type SessionInfo = S["SessionInfo"];
export type Membership = S["Membership"];
export type MyAccount = S["MyAccount"];
export type App = S["App"];
export type AppOverview = S["AppOverview"];
export type ApiKey = S["ApiKey"];
export type Environment = "live" | "sandbox";

export const productKeys = {
  me: ["app", "me"] as const,
  account: ["app", "account"] as const,
  apps: ["app", "apps"] as const,
  app: (id: string) => ["app", "apps", id] as const,
  overview: (id: string, env: Environment) => ["app", "apps", id, "overview", env] as const,
  keys: (id: string) => ["app", "apps", id, "keys"] as const,
};

/** Signs in and keeps the session token of this tab. */
export function useSignIn() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string }) => unwrap(api.POST("/auth/signin", { body })),
    onSuccess: (s) => {
      queries.clear();
      setPersonToken(s.token ?? null);
    },
  });
}

export function useSignUp() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string; display_name: string; account_name: string }) => unwrap(api.POST("/auth/signup", { body })),
    onSuccess: (s) => {
      queries.clear();
      setPersonToken(s.token ?? null);
    },
  });
}

/** Revokes the session at the gateway (best effort: the token is forgotten here either way) and clears everything cached. */
export function useSignOut() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      try {
        await api.POST("/auth/signout");
      } catch {
        // the network is down: the token is dropped locally and expires on its own
      }
    },
    onSettled: () => {
      setPersonToken(null);
      queries.clear();
    },
  });
}

const enabled = computed(() => personToken.value !== null);

export function useMe() {
  return useQuery({ queryKey: productKeys.me, queryFn: () => unwrap(api.GET("/auth/me")), enabled });
}

export function useAccount() {
  return useQuery({ queryKey: productKeys.account, queryFn: () => unwrap(api.GET("/app/v1/account")), enabled });
}

export function useApps() {
  return useQuery({ queryKey: productKeys.apps, queryFn: () => unwrap(api.GET("/app/v1/apps")), enabled });
}

export function useApp(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => productKeys.app(toValue(id))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
  });
}

export function useOverview(id: MaybeRefOrGetter<string>, env: MaybeRefOrGetter<Environment>) {
  return useQuery({
    queryKey: computed(() => productKeys.overview(toValue(id), toValue(env))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/overview", { params: { path: { id: toValue(id) }, query: { environment: toValue(env) } } })),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
    // The numbers move while the page is open (a payment arrives): look again once a minute.
    refetchInterval: 60_000,
  });
}

export function useApiKeys(id: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => productKeys.keys(toValue(id))),
    queryFn: () => unwrap(api.GET("/app/v1/apps/{id}/api-keys", { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => enabled.value && toValue(id) !== ""),
  });
}

export interface NewApp {
  name: string;
  currency: Currency;
  timezone: string;
}

export function useCreateApp() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: NewApp) => unwrap(api.POST("/app/v1/apps", { body })),
    onSuccess: () => queries.invalidateQueries({ queryKey: productKeys.apps }),
  });
}

export function useCreateApiKey(id: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (environment: Environment) => unwrap(api.POST("/app/v1/apps/{id}/api-keys", { params: { path: { id: toValue(id) } }, body: { environment } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: productKeys.keys(toValue(id)) }),
  });
}

export function useRevokeApiKey(id: MaybeRefOrGetter<string>) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) => unwrap(api.DELETE("/app/v1/apps/{id}/api-keys/{key_id}", { params: { path: { id: toValue(id), key_id: keyId } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: productKeys.keys(toValue(id)) }),
  });
}
