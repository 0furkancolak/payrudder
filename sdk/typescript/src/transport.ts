import { ConnectionError, TimeoutError, errorForStatus, type ErrorDetail, type PayrudderError } from "./errors";
import { VERSION } from "./version";

export const DEFAULT_BASE_URL = "http://localhost:42800";

export interface PayrudderOptions {
  /** `qk_test_...` or `qk_live_...`. Falls back to the `PAYRUDDER_API_KEY` environment variable. */
  apiKey?: string;
  /** Falls back to `PAYRUDDER_BASE_URL`, then `http://localhost:42800`. */
  baseUrl?: string;
  /** Per attempt, in milliseconds. Default 30 000. */
  timeout?: number;
  /** Automatic retries after the first attempt. Default 2. */
  maxRetries?: number;
  /** Custom `fetch` (tests, proxies, edge runtimes). Default: the global one. */
  fetch?: typeof fetch;
  /** Default `payrudder-typescript/<version>`. Browsers ignore it. */
  userAgent?: string;
  /** First backoff step in milliseconds; doubles per attempt with jitter. Default 500. */
  initialRetryDelay?: number;
  /** Cap of the backoff in milliseconds. Default 8 000. */
  maxRetryDelay?: number;
  /** Cap on how long a `Retry-After` is honoured, in milliseconds. Default 60 000. */
  maxRetryAfter?: number;
  /** Test hook: replaces the real sleep between attempts. */
  sleep?: (ms: number) => Promise<void>;
  /** Test hook: replaces `Math.random` in the jitter. */
  random?: () => number;
}

export interface RequestOptions {
  /** Sent as `Idempotency-Key`; also makes a POST safe to retry after a 500. Stable across the retries of this call. */
  idempotencyKey?: string;
  /** Overrides the client timeout (milliseconds) for this call. */
  timeout?: number;
  /** Overrides the client's `maxRetries` for this call. */
  maxRetries?: number;
  signal?: AbortSignal;
  /** Extra headers, e.g. `X-Payrudder-Actor`. */
  headers?: Record<string, string>;
}

export interface RequestSpec {
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  path: string;
  query?: object | undefined;
  body?: unknown;
  bodyKind?: "json" | "form";
  response: "json" | "text" | "bytes" | "none";
  /** The operation accepts an `Idempotency-Key` header: one is generated when the caller gave none. */
  idempotencyHeader?: boolean;
  /** Overrides the method-based guess of whether a retry after a 500 is safe (ingest with per-event keys). */
  idempotent?: boolean;
}

export interface Transport {
  request<T>(spec: RequestSpec, options?: RequestOptions): Promise<T>;
}

/** Encodes one path segment. */
export function seg(value: string | number | bigint): string {
  return encodeURIComponent(String(value));
}

interface ResolvedConfig {
  apiKey: string;
  baseUrl: string;
  timeout: number;
  maxRetries: number;
  fetch: typeof fetch;
  userAgent: string;
  initialRetryDelay: number;
  maxRetryDelay: number;
  maxRetryAfter: number;
  sleep: (ms: number) => Promise<void>;
  random: () => number;
}

function env(name: string): string | undefined {
  const proc = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
  const v = proc?.env?.[name];
  return v === undefined || v === "" ? undefined : v;
}

export function resolveConfig(options: PayrudderOptions): ResolvedConfig {
  const apiKey = options.apiKey ?? env("PAYRUDDER_API_KEY");
  if (!apiKey) {
    throw new Error("Payrudder: apiKey is required (pass it or set PAYRUDDER_API_KEY)");
  }
  const maxRetries = options.maxRetries ?? 2;
  const timeout = options.timeout ?? 30_000;
  if (!Number.isInteger(maxRetries) || maxRetries < 0) throw new Error("Payrudder: maxRetries must be a non-negative integer");
  if (!(timeout > 0)) throw new Error("Payrudder: timeout must be positive (milliseconds)");
  return {
    apiKey,
    baseUrl: (options.baseUrl ?? env("PAYRUDDER_BASE_URL") ?? DEFAULT_BASE_URL).replace(/\/+$/, ""),
    timeout,
    maxRetries,
    fetch: options.fetch ?? ((input, init) => globalThis.fetch(input, init)),
    userAgent: options.userAgent ?? `payrudder-typescript/${VERSION}`,
    initialRetryDelay: options.initialRetryDelay ?? 500,
    maxRetryDelay: options.maxRetryDelay ?? 8_000,
    maxRetryAfter: options.maxRetryAfter ?? 60_000,
    sleep: options.sleep ?? ((ms) => new Promise((resolve) => setTimeout(resolve, ms))),
    random: options.random ?? Math.random,
  };
}

/** JSON.stringify that writes `bigint` (int64 inputs) as the JSON string the API expects. */
export function stringifyBody(value: unknown): string {
  return JSON.stringify(value, (_k, v: unknown) => (typeof v === "bigint" ? v.toString() : v));
}

function scalar(v: string | number | boolean | bigint | Date): string {
  if (v instanceof Date) return v.toISOString();
  return String(v);
}

export function buildQuery(query: object | undefined): string {
  if (!query) return "";
  const parts: string[] = [];
  for (const [k, v] of Object.entries(query) as Array<[string, unknown]>) {
    if (v === undefined || v === null) continue;
    for (const item of (Array.isArray(v) ? v : [v]) as Array<Parameters<typeof scalar>[0]>) {
      parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(scalar(item))}`);
    }
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const secs = Number(value);
  if (Number.isFinite(secs) && secs >= 0) return secs;
  const at = Date.parse(value);
  return Number.isNaN(at) ? undefined : Math.max(0, (at - Date.now()) / 1000);
}

function newKey(): string {
  return `sdk_${globalThis.crypto.randomUUID()}`;
}

interface Raw {
  status: number;
  headers: Headers;
  text?: string;
  bytes?: Uint8Array;
}

export class HttpTransport implements Transport {
  constructor(private readonly cfg: ResolvedConfig) {}

  async request<T>(spec: RequestSpec, options: RequestOptions = {}): Promise<T> {
    const cfg = this.cfg;
    const maxRetries = options.maxRetries ?? cfg.maxRetries;

    const headers: Record<string, string> = {
      Authorization: `Bearer ${cfg.apiKey}`,
      Accept: spec.response === "json" || spec.response === "none" ? "application/json" : "*/*",
      "User-Agent": cfg.userAgent,
      ...options.headers,
    };
    let body: string | FormData | undefined;
    if (spec.body !== undefined) {
      if (spec.bodyKind === "form") body = spec.body as FormData;
      else {
        body = stringifyBody(spec.body);
        headers["Content-Type"] = "application/json";
      }
    }
    // Chosen once, so every retry of this call carries the same key.
    const idempotencyKey = options.idempotencyKey ?? (spec.idempotencyHeader && spec.method === "POST" ? newKey() : undefined);
    if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;

    const idempotent =
      spec.idempotent ?? (spec.method === "GET" || spec.method === "PUT" || spec.method === "DELETE" || idempotencyKey !== undefined);
    const url = `${cfg.baseUrl}${spec.path}${buildQuery(spec.query)}`;
    const init = { method: spec.method, headers, body };

    for (let attempt = 0; ; attempt++) {
      let raw: Raw;
      try {
        raw = await this.attempt(url, init, spec.response, options.timeout ?? cfg.timeout, options.signal);
      } catch (err) {
        if (err instanceof ConnectionError && !isAbort(err) && attempt < maxRetries) {
          await cfg.sleep(this.backoff(attempt));
          continue;
        }
        throw err;
      }

      if (raw.status >= 200 && raw.status < 300) {
        return decode<T>(raw, spec.response);
      }

      const error = toError(raw);
      if (attempt < maxRetries && retryableStatus(raw.status, idempotent)) {
        await cfg.sleep(this.delayFor(attempt, error.retryAfter));
        continue;
      }
      throw error;
    }
  }

  private backoff(attempt: number): number {
    const cap = Math.min(this.cfg.maxRetryDelay, this.cfg.initialRetryDelay * 2 ** attempt);
    return cap / 2 + (this.cfg.random() * cap) / 2;
  }

  private delayFor(attempt: number, retryAfter: number | undefined): number {
    if (retryAfter === undefined) return this.backoff(attempt);
    return Math.min(retryAfter * 1000, this.cfg.maxRetryAfter);
  }

  private async attempt(
    url: string,
    init: { method: string; headers: Record<string, string>; body: string | FormData | undefined },
    kind: RequestSpec["response"],
    timeout: number,
    signal: AbortSignal | undefined,
  ): Promise<Raw> {
    const controller = new AbortController();
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, timeout);
    const onAbort = () => controller.abort();
    if (signal) {
      if (signal.aborted) controller.abort();
      else signal.addEventListener("abort", onAbort, { once: true });
    }
    try {
      const res = await this.cfg.fetch(url, { ...init, signal: controller.signal });
      const raw: Raw = { status: res.status, headers: res.headers };
      if (kind === "bytes" && res.ok) raw.bytes = new Uint8Array(await res.arrayBuffer());
      else raw.text = await res.text();
      return raw;
    } catch (err) {
      if (timedOut) throw new TimeoutError(`Request timed out after ${timeout} ms`, err);
      if (signal?.aborted) throw new ConnectionError("Request aborted", err);
      throw new ConnectionError(`Request failed: ${err instanceof Error ? err.message : String(err)}`, err);
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
    }
  }
}

function isAbort(err: ConnectionError): boolean {
  return err.message === "Request aborted";
}

function retryableStatus(status: number, idempotent: boolean): boolean {
  if (status === 429 || status === 502 || status === 503 || status === 504) return true;
  return status === 500 && idempotent;
}

function decode<T>(raw: Raw, kind: RequestSpec["response"]): T {
  if (kind === "none") return undefined as T;
  if (kind === "bytes") return (raw.bytes ?? new Uint8Array()) as T;
  const text = raw.text ?? "";
  if (kind === "text") return text as T;
  if (text === "") return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch (err) {
    throw new ConnectionError("Response was not valid JSON", err);
  }
}

interface Envelope {
  error?: { code?: string; key?: string; message?: string; details?: ErrorDetail[]; request_id?: string };
}

function toError(raw: Raw): PayrudderError {
  const text = raw.text ?? "";
  let parsed: unknown = text;
  try {
    parsed = JSON.parse(text);
  } catch {
    /* not JSON (a proxy's HTML page, say): keep the text */
  }
  const env = (typeof parsed === "object" && parsed !== null ? (parsed as Envelope).error : undefined) ?? {};
  const Ctor = errorForStatus(raw.status);
  return new Ctor({
    status: raw.status,
    code: env.code,
    key: env.key,
    message: env.message ?? (text.slice(0, 200) || `HTTP ${raw.status}`),
    details: env.details,
    requestId: raw.headers.get("x-request-id") ?? env.request_id,
    retryAfter: parseRetryAfter(raw.headers.get("retry-after")),
    body: parsed,
  });
}
