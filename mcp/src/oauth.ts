import { createHash } from "node:crypto";

/**
 * The OAuth mode of the HTTP server (PAYRUDDER_MCP_AUTH=oauth; docs/plans/20-mcp.md, "OAuth design"; default, not agreed). The server is an OAuth 2.1 resource server for the
 * MCP authorization specification (revision 2026-07-28): it publishes Protected Resource Metadata (RFC 9728) that names Payrudder's authorization server (the gateway),
 * answers a request without a valid token with 401 and a WWW-Authenticate challenge that points at that document, checks on every request that the token is active, was issued
 * for this server (its audience, RFC 8707) and has not expired, and answers 403 insufficient_scope for a call the token's scopes do not cover.
 *
 * It never passes the assistant's token on. It introspects the token (RFC 7662) and trades it (RFC 8693) for a credential of a few minutes that the API accepts and that
 * only this process ever holds; the API calls are made with that. Both calls are authenticated with a secret shared with the gateway (PAYRUDDER_MCP_OAUTH_SECRET). The
 * answers are kept for `cacheMs` (30 s), which is also how long a revoked token can still be used here; the expiry is checked on every request regardless.
 */
export interface OAuthOptions {
  /** PAYRUDDER_MCP_PUBLIC_URL: the canonical address of this MCP endpoint, as clients are given it (https://mcp.example.com/mcp). It is the audience of the tokens. */
  publicUrl: string;
  /** PAYRUDDER_MCP_ISSUER: the authorization server's address, the gateway's PAYRUDDER_PUBLIC_URL (default: the API address). */
  issuer: string;
  /** PAYRUDDER_MCP_OAUTH_SECRET: the gateway's PAYRUDDER_OAUTH_RESOURCE_SECRET. */
  secret: string;
  /** Injected in tests. */
  fetch?: typeof fetch;
  now?: () => number;
  /** How long an introspection and its credential are reused; 30 s by default. */
  cacheMs?: number;
}

/** Who a request is, once its token has been checked. */
export interface Principal {
  /** The person (the authorization server's `sub`). */
  sub: string;
  tenantId: string;
  environment: "sandbox" | "live";
  clientId: string;
  clientName: string;
  scopes: string[];
  /** The short-lived credential for the API audience. */
  credential: string;
  /** Unix seconds the access token ends. */
  exp: number;
}

export type Verdict =
  | { ok: true; principal: Principal }
  | { ok: false; status: 401 | 403 | 503; challenge?: string; message: string; retryAfter?: number };

/** What the authorization server gives out as an access token (pkg/oauth: qo_ and 43 characters of base64url). */
const TOKEN = /^qo_[A-Za-z0-9_-]{43}$/;
const MAX_CACHE = 10_000;
const EXCHANGE = "urn:ietf:params:oauth:grant-type:token-exchange";
const ACCESS_TOKEN_TYPE = "urn:ietf:params:oauth:token-type:access_token";

/** The scope asked for in a challenge: the smallest useful set, which the authorization server reads as every :read except the audit log. */
export const DEFAULT_SCOPE = "read_only";

export interface OAuth {
  /** The canonical address of this server: the `resource` of the metadata and the audience of a token. */
  readonly resource: string;
  /** The paths (on this server) that serve the Protected Resource Metadata. */
  readonly metadataPaths: readonly string[];
  metadata(): Record<string, unknown>;
  /** A WWW-Authenticate value. `error` is absent when the request carried no credentials at all (RFC 6750 3.1). */
  challenge(c?: { error?: "invalid_token" | "insufficient_scope"; scope?: string; description?: string }): string;
  /** Checks the Authorization header of one request. */
  authenticate(header: string | undefined): Promise<Verdict>;
}

interface Entry {
  info: Omit<Principal, "credential">;
  checked: number;
  credential?: string;
  credentialEnds?: number;
}

/** The address of the Protected Resource Metadata for a resource, by RFC 9728 3.1: the well-known segment goes between the host and the path. */
export function metadataUrlOf(resource: string): string {
  const u = new URL(resource);
  return `${u.origin}/.well-known/oauth-protected-resource${u.pathname === "/" ? "" : u.pathname}`;
}

/** The canonical form of the public address, or why it cannot be one: https (http only for a loopback host), no query or fragment, no trailing slash. */
export function canonicalResource(raw: string): { ok: true; url: string } | { ok: false; why: string } {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return { ok: false, why: "it is not a URL" };
  }
  const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(u.hostname);
  if (u.protocol !== "https:" && !(u.protocol === "http:" && loopback)) return { ok: false, why: "it must be https (http only for localhost)" };
  if (u.search || u.hash || u.username || u.password) return { ok: false, why: "it must have no query, fragment or credentials" };
  return { ok: true, url: `${u.origin}${u.pathname}`.replace(/\/+$/, "") };
}

const q = (s: string) => s.replace(/["\\]/g, "");

export function createOAuth(o: OAuthOptions): OAuth {
  const doFetch = o.fetch ?? fetch;
  const now = o.now ?? Date.now;
  const cacheMs = o.cacheMs ?? 30_000;
  const resource = o.publicUrl.replace(/\/+$/, "");
  const issuer = o.issuer.replace(/\/+$/, "");
  const prm = metadataUrlOf(resource);
  const cache = new Map<string, Entry>();

  async function post(path: string, form: Record<string, string>): Promise<Response | undefined> {
    try {
      return await doFetch(`${issuer}${path}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${o.secret}`, "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json", "User-Agent": "payrudder-mcp/0.0.0" },
        body: new URLSearchParams(form),
        signal: AbortSignal.timeout(10_000),
      });
    } catch {
      return undefined;
    }
  }
  const unavailable = (message = "The authorization server could not be reached. Try again in a moment."): Verdict => ({ ok: false, status: 503, message, retryAfter: 5 });
  const invalid = (description: string): Verdict => ({ ok: false, status: 401, challenge: challenge({ error: "invalid_token", scope: DEFAULT_SCOPE, description }), message: description });

  function challenge(c: { error?: "invalid_token" | "insufficient_scope"; scope?: string; description?: string } = {}): string {
    const parts = [`resource_metadata="${prm}"`];
    if (c.error) parts.unshift(`error="${c.error}"`);
    if (c.scope) parts.push(`scope="${q(c.scope)}"`);
    if (c.description) parts.push(`error_description="${q(c.description)}"`);
    return `Bearer ${parts.join(", ")}`;
  }

  /** The introspection of a token: undefined when it is unreachable, null when it is not (or no longer) good for this server. */
  async function introspect(token: string): Promise<Omit<Principal, "credential"> | null | undefined> {
    const res = await post("/oauth/introspect", { token, resource });
    if (!res || res.status >= 500 || res.status === 429 || res.status === 401) return undefined; // 401: our own secret is wrong; the person's token is not to blame
    if (!res.ok) return null;
    const j = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    if (!j || j.active !== true) return null;
    // The authorization server has said "active"; this server still checks for itself that the token is for it and has not ended (RFC 8707, MCP: audience binding).
    const aud = Array.isArray(j.aud) ? j.aud : [j.aud];
    const exp = typeof j.exp === "number" ? j.exp : 0;
    const env = j.environment === "live" || j.environment === "sandbox" ? j.environment : undefined;
    if (!aud.includes(resource) || exp * 1000 <= now() || !env || typeof j.sub !== "string" || typeof j.tenant_id !== "string" || typeof j.scope !== "string") return null;
    return { sub: j.sub, tenantId: j.tenant_id, environment: env, clientId: String(j.client_id ?? ""), clientName: String(j.client_name ?? ""), scopes: j.scope.split(" ").filter(Boolean), exp };
  }

  /** The credential for the API: undefined when the authorization server is unreachable, a Verdict when it refuses. */
  async function exchange(token: string): Promise<{ credential: string; ends: number } | Verdict | undefined> {
    const res = await post("/oauth/token", { grant_type: EXCHANGE, subject_token: token, subject_token_type: ACCESS_TOKEN_TYPE, audience: issuer, resource });
    if (!res || res.status >= 500 || res.status === 429) return undefined;
    const j = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    if (res.status === 403) return { ok: false, status: 403, message: "The authorization server does not let an assistant act on this environment." };
    if (res.status === 401 && j?.error === "invalid_client") return undefined;
    if (!res.ok || !j || typeof j.access_token !== "string" || !j.access_token.startsWith("qm_")) return invalid("The access token can no longer be exchanged.");
    return { credential: j.access_token, ends: now() + Math.max(1, Number(j.expires_in) || 60) * 1000 };
  }

  async function authenticate(header: string | undefined): Promise<Verdict> {
    const token = /^Bearer (.+)$/i.exec(header ?? "")?.[1]?.trim();
    if (!token) return { ok: false, status: 401, challenge: challenge({ scope: DEFAULT_SCOPE }), message: "Authorization is required: sign in through the authorization server named in the Protected Resource Metadata." };
    if (!TOKEN.test(token)) return invalid("The token is not an access token of this authorization server.");
    const key = createHash("sha256").update(token).digest("hex");
    let e = cache.get(key);
    if (!e || now() - e.checked >= cacheMs || e.info.exp * 1000 <= now()) {
      cache.delete(key);
      const info = await introspect(token);
      if (info === undefined) return unavailable();
      if (info === null) return invalid("The token is invalid, expired, revoked or was not issued for this server.");
      e = { info, checked: now() };
    }
    // A credential is reused until just before it ends (it lives minutes; the cached answer for seconds).
    if (!e.credential || (e.credentialEnds ?? 0) - 10_000 <= now()) {
      const x = await exchange(token);
      if (x === undefined) return unavailable();
      if ("ok" in x) {
        cache.delete(key);
        return x;
      }
      e.credential = x.credential;
      e.credentialEnds = x.ends;
    }
    cache.delete(key); // re-inserted: the oldest is the one forgotten when the table is full
    cache.set(key, e);
    if (cache.size > MAX_CACHE) cache.delete(cache.keys().next().value as string);
    return { ok: true, principal: { ...e.info, credential: e.credential } };
  }

  const u = new URL(resource);
  const own = `/.well-known/oauth-protected-resource${u.pathname === "/" ? "" : u.pathname}`;
  return {
    resource,
    // The address clients are given, the root form and the form for the local /mcp path, so a proxy that rewrites the path still finds one.
    metadataPaths: [...new Set([own, "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"])],
    metadata: () => ({ resource, authorization_servers: [issuer], scopes_supported: [DEFAULT_SCOPE], bearer_methods_supported: ["header"], resource_name: "Payrudder" }),
    challenge,
    authenticate,
  };
}

/** The scopes the tool calls in a JSON-RPC body need (a single message or a batch), each once; `scopeOf` names a tool's permission, undefined when the API decides. */
export function requiredScopes(body: unknown, scopeOf: (tool: string) => string | undefined): string[] {
  const out = new Set<string>();
  for (const m of Array.isArray(body) ? body : [body]) {
    const msg = m as { method?: unknown; params?: { name?: unknown } } | null;
    if (msg?.method === "tools/call" && typeof msg.params?.name === "string") {
      const s = scopeOf(msg.params.name);
      if (s) out.add(s);
    }
  }
  return [...out];
}
