import { describe, expect, it } from "vitest";
import { canonicalResource, createOAuth, metadataUrlOf, requiredScopes } from "../oauth.js";
import { readPermission } from "../scopes.js";

const RESOURCE = "https://mcp.example.com/mcp";
const ISSUER = "https://api.example.com";
const SECRET = "s".repeat(40);
const T1 = "qo_" + "A".repeat(43);
const T2 = "qo_" + "B".repeat(43);

interface Fake {
  /** token -> the introspection body (active or not); a token not here is inactive. */
  tokens: Map<string, Record<string, unknown>>;
  calls: { path: string; form: URLSearchParams; auth: string | null }[];
  exchangeStatus: number;
  down: boolean;
  minted: number;
}

function fakeAs(now: () => number): { fetch: typeof fetch; s: Fake } {
  const s: Fake = { tokens: new Map(), calls: [], exchangeStatus: 200, down: false, minted: 0 };
  const f = (async (url: URL | string, init: RequestInit = {}) => {
    if (s.down) throw new Error("connection refused");
    const path = new URL(String(url)).pathname;
    const form = new URLSearchParams(String(init.body));
    s.calls.push({ path, form, auth: new Headers(init.headers).get("Authorization") });
    if (new Headers(init.headers).get("Authorization") !== `Bearer ${SECRET}`) return new Response(JSON.stringify({ error: "invalid_client" }), { status: 401 });
    const tok = s.tokens.get(form.get("token") ?? form.get("subject_token") ?? "");
    if (path === "/oauth/introspect") {
      if (!tok || tok.active === false || form.get("resource") !== tok.aud) return new Response(JSON.stringify({ active: false }), { status: 200 });
      return new Response(JSON.stringify(tok), { status: 200 });
    }
    if (path === "/oauth/token") {
      if (s.exchangeStatus !== 200) return new Response(JSON.stringify({ error: "access_denied" }), { status: s.exchangeStatus });
      if (!tok || tok.active === false) return new Response(JSON.stringify({ error: "invalid_grant" }), { status: 401 });
      s.minted += 1;
      return new Response(JSON.stringify({ access_token: `qm_minted${s.minted}`, expires_in: 300, token_type: "Bearer", scope: tok.scope, issued_token_type: "urn:ietf:params:oauth:token-type:access_token" }), { status: 200 });
    }
    return new Response("", { status: 404 });
  }) as typeof fetch;
  void now;
  return { fetch: f, s };
}

const active = (over: Record<string, unknown> = {}) => ({
  active: true, scope: "customers:read invoices:read", client_id: "https://client.example/m.json", client_name: "Example", sub: "user-1", aud: RESOURCE, exp: 4_000_000_000, token_type: "Bearer",
  tenant_id: "tenant-1", environment: "sandbox", member_role: "owner", ...over,
});

function setup(over: { cacheMs?: number } = {}) {
  let t = 1_000_000_000_000;
  const clock = () => t;
  const { fetch, s } = fakeAs(clock);
  s.tokens.set(T1, active());
  const oauth = createOAuth({ publicUrl: RESOURCE, issuer: ISSUER, secret: SECRET, fetch, now: clock, ...over });
  return { oauth, s, advance: (ms: number) => (t += ms) };
}

describe("the metadata and the challenge", () => {
  it("puts the well-known segment between the host and the path of the resource (RFC 9728)", () => {
    expect(metadataUrlOf("https://mcp.example.com/mcp")).toBe("https://mcp.example.com/.well-known/oauth-protected-resource/mcp");
    expect(metadataUrlOf("https://mcp.example.com")).toBe("https://mcp.example.com/.well-known/oauth-protected-resource");
    expect(metadataUrlOf("http://localhost:3000/mcp")).toBe("http://localhost:3000/.well-known/oauth-protected-resource/mcp");
  });

  it("names the resource, the authorization server and the scopes, and is served on the path clients try", () => {
    const { oauth } = setup();
    expect(oauth.metadata()).toMatchObject({ resource: RESOURCE, authorization_servers: [ISSUER], bearer_methods_supported: ["header"] });
    expect(oauth.metadataPaths).toContain("/.well-known/oauth-protected-resource/mcp");
    expect(oauth.metadataPaths).toContain("/.well-known/oauth-protected-resource");
  });

  it("builds a WWW-Authenticate with the metadata address first when there were no credentials, and the error when there were", () => {
    const { oauth } = setup();
    expect(oauth.challenge({ scope: "read_only" })).toBe(`Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp", scope="read_only"`);
    expect(oauth.challenge({ error: "insufficient_scope", scope: "customers:write", description: 'needs "it"' })).toBe(
      `Bearer error="insufficient_scope", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp", scope="customers:write", error_description="needs it"`,
    );
  });
});

describe("the public address", () => {
  it("is https, or http for localhost, with no query, fragment or credentials, and loses its trailing slash", () => {
    expect(canonicalResource("https://mcp.example.com/mcp/")).toEqual({ ok: true, url: "https://mcp.example.com/mcp" });
    expect(canonicalResource("https://mcp.example.com")).toEqual({ ok: true, url: "https://mcp.example.com" });
    expect(canonicalResource("http://localhost:3000/mcp")).toEqual({ ok: true, url: "http://localhost:3000/mcp" });
    for (const bad of ["", "mcp.example.com", "http://mcp.example.com/mcp", "https://mcp.example.com/mcp?x=1", "https://mcp.example.com/mcp#f", "https://u:p@mcp.example.com/mcp"]) {
      expect(canonicalResource(bad).ok, bad).toBe(false);
    }
  });
});

describe("validating a token", () => {
  it("answers a request without credentials with a challenge that points at the metadata and names the scope", async () => {
    const { oauth, s } = setup();
    const v = await oauth.authenticate(undefined);
    expect(v).toMatchObject({ ok: false, status: 401 });
    expect(v.ok === false && v.challenge).toContain('resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"');
    expect(v.ok === false && v.challenge).toContain('scope="read_only"');
    expect(v.ok === false && v.challenge).not.toContain("error=");
    expect(s.calls).toHaveLength(0);
  });

  it("refuses what is not an access token without asking anyone: an API key, a refresh token, the credential for the API, garbage", async () => {
    const { oauth, s } = setup();
    for (const h of ["Bearer qk_test_abcdefgh_" + "x".repeat(40), "Bearer qr_" + "A".repeat(43), "Bearer qm_" + "A".repeat(43), "Bearer nope", "Bearer qo_short", `Bearer ${T1}x`]) {
      const v = await oauth.authenticate(h);
      expect(v, h).toMatchObject({ ok: false, status: 401 });
      expect(v.ok === false && v.challenge, h).toContain('error="invalid_token"');
    }
    expect(s.calls).toHaveLength(0);
  });

  it("introspects with the shared secret and this server's own address, trades the token for a credential, and returns who the caller is", async () => {
    const { oauth, s } = setup();
    const v = await oauth.authenticate(`Bearer ${T1}`);
    expect(v.ok).toBe(true);
    if (!v.ok) return;
    expect(v.principal).toMatchObject({ sub: "user-1", tenantId: "tenant-1", environment: "sandbox", scopes: ["customers:read", "invoices:read"], credential: "qm_minted1", clientId: "https://client.example/m.json" });
    const [intro, ex] = s.calls;
    expect(intro?.path).toBe("/oauth/introspect");
    expect(intro?.auth).toBe(`Bearer ${SECRET}`);
    expect(intro?.form.get("resource")).toBe(RESOURCE);
    expect(intro?.form.get("token")).toBe(T1);
    expect(ex?.path).toBe("/oauth/token");
    expect(ex?.form.get("grant_type")).toBe("urn:ietf:params:oauth:grant-type:token-exchange");
    expect(ex?.form.get("subject_token")).toBe(T1);
    expect(ex?.form.get("audience")).toBe(ISSUER);
    expect(ex?.form.get("resource")).toBe(RESOURCE);
  });

  it("never returns the assistant's token as the credential: the API is called with the minted one", async () => {
    const { oauth } = setup();
    const v = await oauth.authenticate(`Bearer ${T1}`);
    expect(v.ok && v.principal.credential).not.toBe(T1);
    expect(v.ok && v.principal.credential.startsWith("qm_")).toBe(true);
  });

  it("refuses a token that is inactive, unknown, or issued for another resource", async () => {
    const { oauth, s } = setup();
    s.tokens.set(T2, active({ active: false }));
    expect(await oauth.authenticate(`Bearer ${T2}`)).toMatchObject({ ok: false, status: 401 });
    expect(await oauth.authenticate(`Bearer qo_${"C".repeat(43)}`)).toMatchObject({ ok: false, status: 401 });
    // Issued for another server: the authorization server would say inactive for this resource; a server that answered active with another audience is not believed either.
    s.tokens.set(T2, active({ aud: "https://other.example/mcp" }));
    expect(await oauth.authenticate(`Bearer ${T2}`)).toMatchObject({ ok: false, status: 401 });
  });

  it("checks the audience and the expiry of the answer itself, whatever the authorization server says", async () => {
    const liar = createOAuth({
      publicUrl: RESOURCE,
      issuer: ISSUER,
      secret: SECRET,
      now: () => 2_000_000_000_000,
      fetch: (async () => new Response(JSON.stringify(active({ aud: "https://other.example/mcp" })), { status: 200 })) as typeof fetch,
    });
    expect(await liar.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 401 }); // wrong audience
    const old = createOAuth({
      publicUrl: RESOURCE,
      issuer: ISSUER,
      secret: SECRET,
      now: () => 4_000_000_001_000,
      fetch: (async () => new Response(JSON.stringify(active()), { status: 200 })) as typeof fetch,
    });
    expect(await old.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 401 }); // exp has passed
    const bad = createOAuth({ publicUrl: RESOURCE, issuer: ISSUER, secret: SECRET, fetch: (async () => new Response(JSON.stringify(active({ environment: "staging" })), { status: 200 })) as typeof fetch });
    expect(await bad.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 401 }); // not a body of ours
  });

  it("takes an audience given as a list", async () => {
    const listed = createOAuth({ publicUrl: RESOURCE, issuer: ISSUER, secret: SECRET, fetch: (async (u: URL | string) => (String(u).endsWith("introspect") ? new Response(JSON.stringify(active({ aud: [RESOURCE, "x"] })), { status: 200 }) : new Response(JSON.stringify({ access_token: "qm_x", expires_in: 300 }), { status: 200 }))) as typeof fetch });
    expect((await listed.authenticate(`Bearer ${T1}`)).ok).toBe(true);
  });

  it("treats another authentication scheme as no credentials at all", async () => {
    const { oauth } = setup();
    const v = await oauth.authenticate("Basic abc");
    expect(v.ok === false && v.challenge).not.toContain("error=");
  });

  it("stops accepting a token the moment it expires, even from the cache", async () => {
    const { oauth, s, advance } = setup({ cacheMs: 10 * 60_000 });
    s.tokens.set(T2, active({ exp: 1_000_000_000 + 60 })); // ends 60 s after the clock's start
    expect((await oauth.authenticate(`Bearer ${T2}`)).ok).toBe(true);
    advance(30_000);
    expect((await oauth.authenticate(`Bearer ${T2}`)).ok).toBe(true);
    advance(31_000);
    const gone = await oauth.authenticate(`Bearer ${T2}`);
    expect(gone).toMatchObject({ ok: false, status: 401 });
  });

  it("reuses the answer for the cache time, then asks again, so a revoked token stops within it", async () => {
    const { oauth, s, advance } = setup({ cacheMs: 30_000 });
    await oauth.authenticate(`Bearer ${T1}`);
    await oauth.authenticate(`Bearer ${T1}`);
    expect(s.calls.filter((c) => c.path === "/oauth/introspect")).toHaveLength(1);
    s.tokens.set(T1, { active: false });
    expect((await oauth.authenticate(`Bearer ${T1}`)).ok).toBe(true); // still inside the cache time: the documented delay
    advance(30_001);
    expect(await oauth.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 401 });
    expect(await oauth.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 401 }); // and a refused token is not remembered as good
  });

  it("trades for a new credential before the old one ends", async () => {
    const { oauth, s, advance } = setup({ cacheMs: 10 * 60_000 });
    const a = await oauth.authenticate(`Bearer ${T1}`);
    advance(100_000);
    const b = await oauth.authenticate(`Bearer ${T1}`);
    advance(195_000); // 295 s after the first: 5 s before it ends, inside the 10 s margin
    const c = await oauth.authenticate(`Bearer ${T1}`);
    expect([a, b, c].map((v) => v.ok && v.principal.credential)).toEqual(["qm_minted1", "qm_minted1", "qm_minted2"]);
    expect(s.calls.filter((x) => x.path === "/oauth/introspect")).toHaveLength(1);
  });

  it("is a 503 with Retry-After, not a 401, when the authorization server cannot be reached or does not take the secret", async () => {
    const { oauth, s } = setup();
    s.down = true;
    expect(await oauth.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 503, retryAfter: 5 });
    s.down = false;
    const wrong = createOAuth({ publicUrl: RESOURCE, issuer: ISSUER, secret: "x".repeat(40), fetch: fakeAs(Date.now).fetch });
    expect(await wrong.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 503 });
  });

  it("passes on the authorization server's refusal to exchange (a live environment it no longer allows) as a 403", async () => {
    const { oauth, s } = setup();
    s.exchangeStatus = 403;
    expect(await oauth.authenticate(`Bearer ${T1}`)).toMatchObject({ ok: false, status: 403 });
  });
});

describe("the scopes a call needs", () => {
  const scopeOf = (n: string) => ({ get_customer: "customers:read", create_customer: "customers:write", list_meters: "catalog:read", odd_tool: undefined })[n];
  const call = (name: string, id = 1) => ({ jsonrpc: "2.0", id, method: "tools/call", params: { name, arguments: {} } });

  it("lists what each tool call in a message or a batch needs, once, and nothing for other methods", () => {
    expect(requiredScopes(call("get_customer"), scopeOf)).toEqual(["customers:read"]);
    expect(requiredScopes([call("get_customer"), call("create_customer", 2), call("get_customer", 3)], scopeOf)).toEqual(["customers:read", "customers:write"]);
    expect(requiredScopes({ jsonrpc: "2.0", id: 1, method: "tools/list" }, scopeOf)).toEqual([]);
    expect(requiredScopes(call("odd_tool"), scopeOf)).toEqual([]);
    expect(requiredScopes(call("unknown"), scopeOf)).toEqual([]);
    expect(requiredScopes(null, scopeOf)).toEqual([]);
    expect(requiredScopes([null, 3, "x"], scopeOf)).toEqual([]);
  });

  it("derives a read's permission from its path the way the gateway does", () => {
    for (const [path, want] of Object.entries({
      "/v1/customers": "customers:read",
      "/v1/customers/{external_id}": "customers:read",
      "/v1/customers/{external_id}/usage": "usage:read",
      "/v1/customers/{external_id}/entitlements/{meter}": "usage:read",
      "/v1/customers/{external_id}/payments": "invoices:read",
      "/v1/customers/{external_id}/credits": "credits:read",
      "/v1/meters": "catalog:read",
      "/v1/plans/{code}/versions/{version}": "catalog:read",
      "/v1/subscriptions/{id}": "subscriptions:read",
      "/v1/invoices": "invoices:read",
      "/v1/credit-notes": "invoices:read",
      "/v1/reports/revenue": "reports:read",
      "/v1/accounting/summary": "accounting:read",
      "/v1/tax-rates": "settings:read",
      "/v1/dunning-settings": "settings:read",
      "/v1/credits/settings": "settings:read",
      "/v1/audit-events": "audit:read",
      "/v1/nothing-like-this": undefined,
    })) {
      expect(readPermission(path), path).toBe(want);
    }
  });
});
