import createClient, { type Middleware } from "openapi-fetch";
import { activeAccount, currentToken, sessionLost } from "@/shared/lib/session";
import type { components, paths } from "./schema";

// Same origin: the console talks to the gateway through its own /gw path (the Cloudflare Worker in production, Vite in development), so the gateway
// needs no CORS and the browser never holds a URL it cannot reach. Only the gateway is used: no database or broker access.
export const api = createClient<paths>({ baseUrl: "/gw" });

/** The product routes that are about "an account": the person's chosen one is named, else the gateway takes their first. */
const ACCOUNT_ROUTE = /^\/gw\/app\/v1\/(account(\/.*)?|apps)$/;

/** Puts the session token of this tab on a request, if there is one. */
function withToken(request: Request): void {
  const token = currentToken();
  if (token) request.headers.set("Authorization", `Bearer ${token}`);
}

/** A 401 while a person is signed in means the session is over (expired, revoked): the app is told once and goes to the sign-in page. */
const endSessionOn401: Pick<Middleware, "onResponse"> = {
  onResponse({ response }) {
    if (response.status === 401 && !isAdminCall(response)) sessionLost();
    return response;
  },
};

api.use({
  ...endSessionOn401,
  onRequest({ request }) {
    withToken(request);
    const url = new URL(request.url);
    if (activeAccount.value && ACCOUNT_ROUTE.test(url.pathname) && !url.searchParams.has("account_id")) {
      url.searchParams.set("account_id", activeAccount.value);
      return new Request(url, request);
    }
    return request;
  },
});

/** The admin API answers 401 to anybody who is not the operator, a signed-in person included: that says nothing about the person's session. */
function isAdminCall(response: Response): boolean {
  try {
    return new URL(response.url).pathname.startsWith("/gw/admin/");
  } catch {
    return false;
  }
}

const asTenantClients = new Map<string, ReturnType<typeof createClient<paths>>>();

/**
 * A client that speaks the tenant API for one tenant: the session token plus the tenant to act as (an operator's session may act as any tenant, a person's
 * only as the tenants of their own apps; the gateway decides). The console shows a
 * tenant's customers, packages and reports through the same endpoints the tenant itself uses.
 */
export function tenantApi(tenantId: string) {
  let client = asTenantClients.get(tenantId);
  if (!client) {
    client = createClient<paths>({ baseUrl: "/gw" });
    client.use({
      ...endSessionOn401,
      onRequest({ request }) {
        withToken(request);
        request.headers.set("X-Payrudder-Tenant", tenantId);
        return request;
      },
    });
    asTenantClients.set(tenantId, client);
  }
  return client;
}

/** A client that speaks as one tenant (its API key) instead of as the operator. */
export const asTenant = (key: string) => createClient<paths>({ baseUrl: "/gw", headers: { Authorization: `Bearer ${key}` } });

export type Tenant = components["schemas"]["Tenant"];
export type Plan = components["schemas"]["Plan"];
export type PlanVersion = components["schemas"]["PlanVersion"];
export type Subscription = components["schemas"]["Subscription"];
export type Invoice = components["schemas"]["Invoice"];

/** An error from the gateway: its envelope's code and message, or a generic one. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: string,
    /** The stable error key (billing.credit_note.not_paid): translate it, not the message. Empty when the server sent none. */
    readonly key = "",
    /** Values a translated message may use ({field}, {detail}), taken from the envelope's first detail. */
    readonly params: Readonly<Record<string, string>> = {},
  ) {
    super(message);
  }
}

export function toApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error;
  if (error && typeof error === "object" && "error" in error) {
    const e = (error as { error?: { code?: string; key?: string; message?: string; details?: { field?: string; description?: string }[] } }).error;
    if (e?.message) {
      const first = e.details?.[0];
      return new ApiError(e.message, e.code ?? "ERROR", e.key ?? "", first ? { field: first.field ?? "", detail: first.description ?? "" } : {});
    }
  }
  return new ApiError(error instanceof Error ? error.message : "request failed", "ERROR");
}

/** Turns an openapi-fetch result into its data, or throws an ApiError (what TanStack Query wants from a query function). */
export async function unwrap<T>(call: Promise<{ data?: T; error?: unknown }>): Promise<T> {
  let r: { data?: T; error?: unknown };
  try {
    r = await call;
  } catch (e) {
    throw toApiError(e);
  }
  if (r.error !== undefined) throw toApiError(r.error);
  return (r.data ?? ({} as T)) as T;
}
