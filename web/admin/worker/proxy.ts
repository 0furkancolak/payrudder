// Pure request logic of the console Worker: which paths belong to the API, how a request is rewritten for the gateway and how the
// generated /env.js looks. No Cloudflare bindings in here, so vitest runs it under plain Node.

/** The path the browser calls the API under. The console's client uses it (src/shared/api/client.ts); it is stripped before the gateway sees the request. */
export const API_PREFIX = "/gw";

/** Gateway routes that are also served as they are (invoice links, health), so a customer-facing link on the console's domain still works. */
const PASSTHROUGH = ["/v1", "/auth", "/r"];
const PASSTHROUGH_EXACT = ["/healthz"];

/** Headers that describe one connection, not the message (RFC 9110 section 7.6.1), plus the ones the platform owns. */
const HOP_BY_HOP = [
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
];

export interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
  /** Where the gateway answers (https://api.example.com), no trailing slash needed. */
  API_ORIGIN: string;
  BRAND_NAME?: string;
  /** "true" shows the demo banner. */
  CONSOLE_DEMO?: string;
}

function under(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(prefix + "/");
}

/** True when the request goes to the gateway; everything else is a file of the single page app. */
export function isApiPath(path: string): boolean {
  return under(path, API_PREFIX) || PASSTHROUGH.some((p) => under(path, p)) || PASSTHROUGH_EXACT.includes(path);
}

/** The gateway URL for a console URL: /gw/auth/me?x=1 becomes <origin>/auth/me?x=1; the other API paths are appended unchanged. */
export function upstreamUrl(apiOrigin: string, url: URL): URL {
  const path = under(url.pathname, API_PREFIX) ? url.pathname.slice(API_PREFIX.length) || "/" : url.pathname;
  const target = new URL(apiOrigin.replace(/\/+$/, "") + path);
  target.search = url.search;
  return target;
}

/** The headers sent to the gateway: the visitor's own (cookies, Authorization) without hop-by-hop ones, and X-Forwarded-For overwritten with the visitor's address. */
export function upstreamHeaders(incoming: Headers): Headers {
  const out = new Headers(incoming);
  // Names listed in Connection are hop-by-hop too.
  for (const name of (incoming.get("connection") ?? "").split(",")) if (name.trim()) out.delete(name.trim());
  for (const name of HOP_BY_HOP) out.delete(name);
  // Overwritten, never appended: this Worker is the edge, so the visitor cannot choose their own rate-limit bucket.
  out.delete("x-forwarded-for");
  out.delete("x-real-ip");
  const ip = incoming.get("cf-connecting-ip");
  if (ip) out.set("x-forwarded-for", ip);
  out.set("x-forwarded-proto", "https");
  const host = incoming.get("host");
  if (host) out.set("x-forwarded-host", host);
  return out;
}

/** The response headers handed back: the gateway's without hop-by-hop ones. Set-Cookie stays (several values survive in Headers). */
export function downstreamHeaders(upstream: Headers): Headers {
  const out = new Headers(upstream);
  for (const name of HOP_BY_HOP) if (name !== "content-length") out.delete(name);
  out.set("cache-control", upstream.get("cache-control") ?? "no-store");
  return out;
}

/** /env.js: the runtime settings the page reads (window.__QUOTIFY__), the Worker's counterpart of what the container generated from its environment. */
export function envScript(env: Pick<Env, "BRAND_NAME" | "CONSOLE_DEMO">): string {
  const brand = (env.BRAND_NAME ?? "").replace(/[^\p{L}\p{N} ._-]/gu, "");
  return `window.__QUOTIFY__=${JSON.stringify({ prefill: false, brand, demo: env.CONSOLE_DEMO === "true" })};`;
}

export async function handle(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  if (url.pathname === "/env.js") {
    return new Response(envScript(env), { headers: { "content-type": "application/javascript", "cache-control": "no-store" } });
  }
  if (!isApiPath(url.pathname)) return env.ASSETS.fetch(request);
  if (!env.API_ORIGIN) return new Response("API_ORIGIN is not configured", { status: 500 });

  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const upstream = await fetch(
    new Request(upstreamUrl(env.API_ORIGIN, url), {
      method: request.method,
      headers: upstreamHeaders(request.headers),
      body: hasBody ? request.body : undefined,
      redirect: "manual", // Redirects (Google sign-in) belong to the browser.
      duplex: "half", // Streaming a body needs it in the fetch standard (Node); workerd accepts and ignores it.
    } as RequestInit),
  );
  return new Response(upstream.body, { status: upstream.status, statusText: upstream.statusText, headers: downstreamHeaders(upstream.headers) });
}
