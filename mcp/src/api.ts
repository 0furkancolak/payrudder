/** A small client of the Payrudder tenant API: one bearer key, JSON in and out, a timeout, and errors that keep the API's own error key. */
export interface ApiOptions {
  baseUrl: string;
  apiKey: string;
  /** Injected in tests; the global fetch otherwise. */
  fetch?: typeof fetch;
  timeoutMs?: number;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly key: string,
    message: string,
  ) {
    super(message);
  }
}

export interface Api {
  get(path: string, query?: Record<string, string | number | undefined>): Promise<unknown>;
  post(path: string, body?: unknown): Promise<unknown>;
}

export function createApi(o: ApiOptions): Api {
  const base = o.baseUrl.replace(/\/+$/, "");
  const doFetch = o.fetch ?? fetch;
  async function call(method: "GET" | "POST", path: string, query?: Record<string, string | number | undefined>, body?: unknown): Promise<unknown> {
    const url = new URL(base + path);
    for (const [k, v] of Object.entries(query ?? {})) if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    const res = await doFetch(url, {
      method,
      headers: { Authorization: `Bearer ${o.apiKey}`, Accept: "application/json", "User-Agent": "payrudder-mcp/0.0.0", ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
      signal: AbortSignal.timeout(o.timeoutMs ?? 30_000),
    });
    const text = await res.text();
    let json: unknown = null;
    try {
      json = text ? JSON.parse(text) : null;
    } catch {
      /* not JSON: reported below with the status */
    }
    if (!res.ok) {
      const e = (json as { error?: { key?: string; message?: string } } | null)?.error;
      throw new ApiError(res.status, e?.key ?? `http.${res.status}`, e?.message ?? `The API answered ${res.status}.`);
    }
    return json;
  }
  return { get: (p, q) => call("GET", p, q), post: (p, b) => call("POST", p, undefined, b ?? {}) };
}

/** A path segment from a value the model supplied: encoded, so `../` or `?x=` in it cannot reach another route. */
export const seg = (v: string): string => encodeURIComponent(v);
