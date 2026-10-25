import { afterEach, describe, expect, it, vi } from "vitest";
import { downstreamHeaders, envScript, handle, isApiPath, upstreamHeaders, upstreamUrl, type Env } from "./proxy";

const assets = (body = "<html>spa</html>") => ({ fetch: vi.fn(async () => new Response(body, { headers: { "content-type": "text/html" } })) });
const envOf = (over: Partial<Env> = {}): Env => ({ ASSETS: assets(), API_ORIGIN: "https://api.example.com/", ...over });

afterEach(() => vi.unstubAllGlobals());

describe("isApiPath", () => {
  it.each(["/gw", "/gw/auth/me", "/v1/invoices", "/auth/google/callback", "/r/abc", "/healthz"])("%s goes to the gateway", (p) => {
    expect(isApiPath(p)).toBe(true);
  });
  it.each(["/", "/app/apps/1/keys", "/signin", "/assets/index-abc.js", "/gwx", "/v1x", "/healthz/extra", "/authors"])("%s is the single page app", (p) => {
    expect(isApiPath(p)).toBe(false);
  });
});

describe("upstreamUrl", () => {
  it("strips /gw and keeps the query", () => {
    expect(upstreamUrl("https://api.example.com/", new URL("https://c.example/gw/app/v1/account?account_id=a1")).href).toBe(
      "https://api.example.com/app/v1/account?account_id=a1",
    );
  });
  it("maps a bare /gw to the root and leaves other API paths alone", () => {
    expect(upstreamUrl("https://api.example.com", new URL("https://c.example/gw")).href).toBe("https://api.example.com/");
    expect(upstreamUrl("https://api.example.com", new URL("https://c.example/r/tok")).href).toBe("https://api.example.com/r/tok");
  });
});

describe("headers", () => {
  it("drops hop-by-hop headers, keeps cookies and Authorization, overwrites X-Forwarded-For", () => {
    const out = upstreamHeaders(
      new Headers({
        cookie: "s=1",
        authorization: "Bearer t",
        connection: "keep-alive, x-custom",
        "x-custom": "1",
        "keep-alive": "timeout=5",
        te: "trailers",
        "x-forwarded-for": "6.6.6.6",
        "cf-connecting-ip": "203.0.113.9",
        host: "c.example",
      }),
    );
    expect(out.get("cookie")).toBe("s=1");
    expect(out.get("authorization")).toBe("Bearer t");
    expect(out.get("x-forwarded-for")).toBe("203.0.113.9");
    for (const h of ["connection", "keep-alive", "te", "x-custom", "host"]) expect(out.has(h)).toBe(false);
    expect(out.get("x-forwarded-host")).toBe("c.example");
  });
  it("sends no X-Forwarded-For when Cloudflare gave no address (a visitor's own value is never believed)", () => {
    expect(upstreamHeaders(new Headers({ "x-forwarded-for": "6.6.6.6" })).has("x-forwarded-for")).toBe(false);
  });
  it("keeps every Set-Cookie of the response and defaults to no-store", () => {
    const up = new Headers();
    up.append("set-cookie", "a=1; Path=/; HttpOnly");
    up.append("set-cookie", "b=2; Path=/");
    up.set("transfer-encoding", "chunked");
    const out = downstreamHeaders(up);
    expect(out.getSetCookie()).toHaveLength(2);
    expect(out.has("transfer-encoding")).toBe(false);
    expect(out.get("cache-control")).toBe("no-store");
  });
});

describe("envScript", () => {
  it("renders the settings and strips characters that could break out of the string", () => {
    const js = envScript({ BRAND_NAME: 'Pay"rudder</script>', CONSOLE_DEMO: "true" });
    expect(js).toBe('window.__QUOTIFY__={"prefill":false,"brand":"Payrudderscript","demo":true};');
  });
});

describe("handle", () => {
  it("serves everything that is not an API path from the assets binding (the SPA fallback is the assets' job)", async () => {
    const env = envOf();
    const res = await handle(new Request("https://c.example/app/members"), env);
    expect(await res.text()).toBe("<html>spa</html>");
    expect(env.ASSETS.fetch).toHaveBeenCalledOnce();
  });

  it("serves /env.js without touching assets or the gateway", async () => {
    const env = envOf({ BRAND_NAME: "Payrudder" });
    const res = await handle(new Request("https://c.example/env.js"), env);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.text()).toContain('"brand":"Payrudder"');
    expect(env.ASSETS.fetch).not.toHaveBeenCalled();
  });

  it("proxies method, body, query and headers to API_ORIGIN and returns status and cookies", async () => {
    const seen: Request[] = [];
    vi.stubGlobal("fetch", async (r: Request) => {
      seen.push(r);
      const h = new Headers({ "content-type": "application/json" });
      h.append("set-cookie", "sid=1; HttpOnly");
      return new Response('{"ok":true}', { status: 201, headers: h });
    });
    const env = envOf();
    const res = await handle(
      new Request("https://c.example/gw/auth/signin?next=%2Fapp", {
        method: "POST",
        body: '{"email":"a@b.c"}',
        headers: { "content-type": "application/json", "cf-connecting-ip": "198.51.100.4", cookie: "x=1" },
      }),
      env,
    );
    expect(res.status).toBe(201);
    expect(res.headers.get("set-cookie")).toContain("sid=1");
    expect(env.ASSETS.fetch).not.toHaveBeenCalled();
    const r = seen[0]!;
    expect(r.url).toBe("https://api.example.com/auth/signin?next=%2Fapp");
    expect(r.method).toBe("POST");
    expect(await r.text()).toBe('{"email":"a@b.c"}');
    expect(r.headers.get("x-forwarded-for")).toBe("198.51.100.4");
    expect(r.headers.get("cookie")).toBe("x=1");
    expect(r.redirect).toBe("manual");
  });

  it("fails loudly when API_ORIGIN is missing", async () => {
    const res = await handle(new Request("https://c.example/gw/x"), envOf({ API_ORIGIN: "" }));
    expect(res.status).toBe(500);
  });
});
