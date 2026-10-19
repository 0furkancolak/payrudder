import type { NextRequest } from "next/server";

// A runtime proxy to the gateway (GATEWAY_URL), so the browser talks to its own origin. It forwards the method, the
// Authorization header and the body, and nothing else.
const gateway = (): string => (process.env.GATEWAY_URL ?? "http://localhost:8000").replace(/\/$/, "");

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }): Promise<Response> {
  const { path } = await ctx.params;
  const url = `${gateway()}/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const headers = new Headers();
  const auth = req.headers.get("authorization");
  if (auth) headers.set("authorization", auth);
  const type = req.headers.get("content-type");
  if (type) headers.set("content-type", type);
  const hasBody = req.method !== "GET" && req.method !== "HEAD";
  const upstream = await fetch(url, { method: req.method, headers, body: hasBody ? await req.arrayBuffer() : undefined, cache: "no-store" });
  const out = new Headers();
  const ct = upstream.headers.get("content-type");
  if (ct) out.set("content-type", ct);
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export const GET = proxy;
export const POST = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
