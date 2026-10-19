import createClient from "openapi-fetch";
import { currentToken } from "@/shared/lib/session";
import type { components, paths } from "./schema";

// Same origin: the console talks to the gateway through its own /gw path (nginx in the image, Vite in development), so the gateway
// needs no CORS and the browser never holds a URL it cannot reach. Only the gateway is used: no database or broker access.
export const api = createClient<paths>({ baseUrl: "/gw" });

api.use({
  onRequest({ request }) {
    const token = currentToken();
    if (token) request.headers.set("Authorization", `Bearer ${token}`);
    return request;
  },
});

/** A client that speaks as one tenant (its API key) instead of as the operator. */
export const asTenant = (key: string) => createClient<paths>({ baseUrl: "/gw", headers: { Authorization: `Bearer ${key}` } });

export type Tenant = components["schemas"]["Tenant"];
export type Invoice = components["schemas"]["Invoice"];
export type InvoiceLine = components["schemas"]["InvoiceLine"];
export type InvoiceSaga = components["schemas"]["InvoiceSaga"];
export type SagaEvent = components["schemas"]["SagaEvent"];

/** An error from the gateway: its envelope's code and message, or a generic one. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: string,
  ) {
    super(message);
  }
}

export function toApiError(error: unknown): ApiError {
  if (error instanceof ApiError) return error;
  if (error && typeof error === "object" && "error" in error) {
    const e = (error as { error?: { code?: string; message?: string } }).error;
    if (e?.message) return new ApiError(e.message, e.code ?? "ERROR");
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
