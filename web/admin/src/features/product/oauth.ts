import { useMutation, useQuery, useQueryClient } from "@tanstack/vue-query";
import { computed } from "vue";
import { api, unwrap } from "@/shared/api/client";
import type { components } from "@/shared/api/schema";
import { personToken } from "@/shared/lib/session";
import { KEY_RESOURCES, type KeyPermission } from "./apiKeys";

// An assistant asks to act for the person (OAuth 2.1 authorization code with PKCE; docs/plans/20-mcp.md, "OAuth design", default, not agreed). The gateway validates the request
// and fetches the client's metadata document; this side only shows it and sends the person's decision. The page never builds the address the browser goes to: it
// opens `redirect_to`, which the gateway made from the client's own validated redirect URI.

export type OAuthRequest = components["schemas"]["OAuthRequest"];
export type OAuthGrant = components["schemas"]["OAuthGrant"];

/** The parameters of an authorization request, in the order the gateway documents them. Anything else in the address is not passed on. */
export const OAUTH_PARAMS = ["client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "scope", "resource", "state"] as const;
export type OAuthParams = Partial<Record<(typeof OAUTH_PARAMS)[number], string>>;

/** The authorization request in a query string. Read from the address itself, not from the router, which would turn a numeric `state` into a number. */
export function oauthParams(search: string): OAuthParams {
  const q = new URLSearchParams(search);
  const out: OAuthParams = {};
  for (const k of OAUTH_PARAMS) {
    const v = q.get(k);
    if (v !== null) out[k] = v;
  }
  return out;
}

export const oauthKeys = { grants: ["auth", "oauth", "grants"] as const };

/** What the request asks for, validated by the gateway; a request it cannot use is an error the page shows (nobody is redirected). */
export function useOAuthRequest(params: OAuthParams) {
  return useQuery({
    queryKey: ["auth", "oauth", "request", params],
    queryFn: () => unwrap(api.GET("/app/v1/oauth/request", { params: { query: params as never } })),
    enabled: computed(() => personToken.value !== null),
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  });
}

/** The person's decision. The answer is the address to open. */
export function useDecideConsent() {
  return useMutation({
    gcTime: 0,
    mutationFn: (v: { params: OAuthParams; decision: "allow" | "deny"; tenantId?: string; scopes?: KeyPermission[] }) =>
      unwrap(
        api.POST("/app/v1/oauth/consent", {
          body: { ...(v.params as Omit<components["schemas"]["OAuthConsentRequest"], "decision">), decision: v.decision, ...(v.tenantId ? { tenant_id: v.tenantId } : {}), ...(v.scopes ? { scopes: v.scopes } : {}) },
        }),
      ),
  });
}

export function useOAuthGrants() {
  return useQuery({
    queryKey: oauthKeys.grants,
    queryFn: () => unwrap(api.GET("/app/v1/oauth/grants")),
    enabled: computed(() => personToken.value !== null),
    // A deployment without OAuth answers 404 here: the security page then shows nothing.
    retry: false,
  });
}

export function useRevokeOAuthGrant() {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/app/v1/oauth/grants/{id}", { params: { path: { id } } })),
    onSuccess: () => queries.invalidateQueries({ queryKey: oauthKeys.grants }),
  });
}

/** The scopes asked for, grouped by the resource they belong to, in the order the API keys page shows them. */
export function groupScopes(scopes: readonly string[]): { resource: string; read?: KeyPermission; write?: KeyPermission }[] {
  const has = new Set(scopes);
  return KEY_RESOURCES.flatMap((r) => {
    const read = r.read && has.has(r.read) ? r.read : undefined;
    const write = r.write && has.has(r.write) ? r.write : undefined;
    return read || write ? [{ resource: r.resource, ...(read ? { read } : {}), ...(write ? { write } : {}) }] : [];
  });
}
