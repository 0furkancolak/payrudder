import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/vue-query";
import { computed, toValue, type MaybeRefOrGetter } from "vue";
import { useI18n } from "vue-i18n";
import { api, unwrap } from "@/shared/api/client";
import { POLL } from "@/shared/api/limits";
import type { components } from "@/shared/api/schema";
import type { Currency } from "@/shared/lib/currencies";
import type { Environment } from "@/shared/lib/environment";
import { toApiLocale } from "@/shared/lib/locale";
import { activeAccount, personToken, setActiveAccount, setOperatorFlag, setPersonToken } from "@/shared/lib/session";
import { AFFILIATE_ROLE, ownMemberships } from "./lib";

// The product API (/auth, /app/v1): what a signed-in person sees in their accounts. Types come from the OpenAPI file.

type S = components["schemas"];
export type MyAccount = S["MyAccount"];
export type App = S["App"];
export type AppOverview = S["AppOverview"];
export type ApiKey = S["ApiKey"];
export type Invitation = S["Invitation"];
export type Role = "owner" | "admin" | "viewer";
export type { Environment };

export const productKeys = {
  me: ["app", "me"] as const,
  account: ["app", "account"] as const,
  apps: ["app", "apps"] as const,
  authMethods: ["auth", "methods"] as const,
  app: (id: string) => ["app", "apps", id] as const,
  overview: (id: string, env: Environment) => ["app", "apps", id, "overview", env] as const,
  keys: (id: string) => ["app", "apps", id, "keys"] as const,
  invitations: ["app", "account", "invitations"] as const,
};

/** A new session starts from nothing cached (another person's data must not show) and keeps the token of this tab. */
function startSession(queries: QueryClient, s: { token?: string; operator?: boolean }): void {
  queries.clear();
  setPersonToken(s.token ?? null, s.operator === true);
}

/** Signs in and keeps the session token of this tab. */
export function useSignIn() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string }) => unwrap(api.POST("/auth/signin", { body })),
    onSuccess: (s) => {
      startSession(queries, s);
    },
  });
}

/**
 * Signs up. The gateway answers 202 for a free and for a taken address alike and opens no session, so the page then signs in with the password just
 * typed: for a new address that works at once (the person may use the app while the address is unverified); for a taken one it fails, and the page shows
 * the same "check your inbox" message as ever (the owner was mailed a warning).
 */
export function useSignUp() {
  const queries = useQueryClient();
  const { locale } = useI18n();
  return useMutation({
    mutationFn: async (body: { email: string; password: string; display_name: string; account_name: string }) => {
      await unwrap(api.POST("/auth/signup", { body: { ...body, locale: toApiLocale(locale.value) } }));
      try {
        const s = await unwrap(api.POST("/auth/signin", { body: { email: body.email, password: body.password } }));
        startSession(queries, s);
        return { signedIn: true };
      } catch {
        return { signedIn: false };
      }
    },
  });
}

/** Spends the token of a verification link. */
export function useVerifyEmail() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (token: string) => unwrap(api.POST("/auth/verify", { body: { token } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: productKeys.me }),
  });
}

/** Asks for a new verification link (the gateway allows one a minute). */
export function useResendVerification() {
  return useMutation({ mutationFn: () => unwrap(api.POST("/auth/resend-verification")) });
}

/** "Forgot password": the answer is the same for every address. */
export function useForgotPassword() {
  const { locale } = useI18n();
  return useMutation({ mutationFn: (email: string) => unwrap(api.POST("/auth/forgot", { body: { email, locale: toApiLocale(locale.value) } })) });
}

export function useResetPassword() {
  return useMutation({ mutationFn: (v: { token: string; password: string }) => unwrap(api.POST("/auth/reset", { body: v })) });
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
  const queries = useQueryClient();
  return useQuery({
    queryKey: productKeys.me,
    queryFn: async () => {
      const me = await unwrap(api.GET("/auth/me"));
      // The server's verdict on whether this person is a platform operator decides whether the shell shows the operator console.
      setOperatorFlag(me.operator === true);
      // The account routes act on the person's first membership unless one is named. When that is a seller's account (the role affiliate: a partner of the
      // seller, not a member of anything) or a chosen account the person no longer belongs to, name their first own account instead.
      const own = ownMemberships(me.memberships);
      const chosen = activeAccount.value;
      if (own.length > 0 && ((me.memberships?.[0]?.role as string | undefined) === AFFILIATE_ROLE || (chosen && !own.some((m) => m.account_id === chosen)))) {
        setActiveAccount(own[0]?.account_id ?? null);
        void queries.invalidateQueries({ queryKey: ["app", "account"] });
        void queries.invalidateQueries({ queryKey: ["app", "apps"] });
      }
      return me;
    },
    enabled,
  });
}

/** Which sign-in methods this deployment offers (the Google button shows only when it is set up). Password and one-time codes are always on. */
export function useAuthMethods() {
  return useQuery({
    queryKey: productKeys.authMethods,
    queryFn: () => unwrap(api.GET("/auth/methods")),
    staleTime: 5 * 60_000,
    // The page works while this loads or fails: the password form is always there.
    retry: false,
  });
}

/** Asks for a one-time code by mail. The answer is the same whether the address has an account or not. */
export function useRequestCode() {
  const { locale } = useI18n();
  return useMutation({ mutationFn: (email: string) => unwrap(api.POST("/auth/otp/request", { body: { email, locale: toApiLocale(locale.value) } })) });
}

/** Signs in (or signs up, for an address without an account) with the code that was mailed; keeps the session token of this tab. */
export function useVerifyCode() {
  const queries = useQueryClient();
  const { locale } = useI18n();
  return useMutation({
    mutationFn: (v: { email: string; code: string; display_name?: string; account_name?: string }) =>
      unwrap(api.POST("/auth/otp/verify", { body: { ...v, locale: toApiLocale(locale.value) } })),
    onSuccess: (s) => {
      startSession(queries, s);
    },
  });
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
    refetchInterval: POLL.figures,
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

// --- members and invitations (phase 39). The account is the person's chosen one (see shared/api/client.ts); the server checks the role again.

function refreshAccount(queries: ReturnType<typeof useQueryClient>) {
  return Promise.all([queries.invalidateQueries({ queryKey: productKeys.account }), queries.invalidateQueries({ queryKey: productKeys.invitations }), queries.invalidateQueries({ queryKey: productKeys.me })]);
}

/** The open invitations of the account; only owners and administrators may ask, so it is enabled for them alone. */
export function useInvitations(enabledFor: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: productKeys.invitations,
    queryFn: () => unwrap(api.GET("/app/v1/account/invitations")),
    enabled: computed(() => enabled.value && toValue(enabledFor)),
  });
}

export function useInviteMember() {
  const queries = useQueryClient();
  const { locale } = useI18n();
  return useMutation({
    mutationFn: (body: { email: string; role: Role }) => unwrap(api.POST("/app/v1/account/invitations", { body: { ...body, locale: toApiLocale(locale.value) } })),
    onSuccess: () => refreshAccount(queries),
  });
}

export function useRevokeInvitation() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/app/v1/account/invitations/{id}", { params: { path: { id } } })),
    onSuccess: () => refreshAccount(queries),
  });
}

export function useResendInvitation() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.POST("/app/v1/account/invitations/{id}/resend", { params: { path: { id } } })),
    onSuccess: () => refreshAccount(queries),
  });
}

export function useChangeRole() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (v: { userId: string; role: Role }) => unwrap(api.PATCH("/app/v1/account/members/{user_id}", { params: { path: { user_id: v.userId } }, body: { role: v.role } })),
    onSuccess: () => refreshAccount(queries),
  });
}

/** Removes a member, or (with the person's own id) leaves the account. */
export function useRemoveMember() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) => unwrap(api.DELETE("/app/v1/account/members/{user_id}", { params: { path: { user_id: userId } } })),
    onSuccess: () => refreshAccount(queries),
  });
}

export function useRenameAccount() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => unwrap(api.PATCH("/app/v1/account", { body: { name } })),
    onSuccess: () => refreshAccount(queries),
  });
}

/** What the invitation of a mailed link is for (public: the token is the credential). */
export function useLookupInvitation() {
  return useMutation({ mutationFn: (token: string) => unwrap(api.POST("/auth/invitations/lookup", { body: { token } })) });
}

/** Joins as a new person: the answer is a session, kept like a sign-in's. */
export function useAcceptInvitation() {
  const queries = useQueryClient();
  const { locale } = useI18n();
  return useMutation({
    mutationFn: (v: { token: string; password: string; displayName: string }) =>
      unwrap(api.POST("/auth/invitations/accept", { body: { token: v.token, password: v.password, display_name: v.displayName, locale: toApiLocale(locale.value) } })),
    onSuccess: (s) => {
      startSession(queries, s);
    },
  });
}

/** Joins as the signed-in person; the account they joined becomes the one they work in. */
export function useAcceptInvitationAsMe() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (token: string) => unwrap(api.POST("/app/v1/invitations/accept", { body: { token } })),
    onSuccess: async (r) => {
      if (r.account_id) setActiveAccount(r.account_id);
      await queries.invalidateQueries({ queryKey: ["app"] });
    },
  });
}

/** Switches the account the person works in; everything that belongs to an account is read again. */
/** After the person left an account: the choice is forgotten (the next account, if any, becomes the first) and everything cached for the old one is read again. */
export function leaveAccount(queries: QueryClient): void {
  setActiveAccount(null);
  void queries.invalidateQueries({ queryKey: ["app"] });
}

export function useSwitchAccount() {
  const queries = useQueryClient();
  return (id: string) => {
    setActiveAccount(id);
    void queries.invalidateQueries({ queryKey: ["app"] });
  };
}
