import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

const TOKEN_KEY = "quotify.operatorToken";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.sessionStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string | null): void {
  if (token === null) window.sessionStorage.removeItem(TOKEN_KEY);
  else window.sessionStorage.setItem(TOKEN_KEY, token);
}

// Same-origin: the console talks to the gateway through its own /gw proxy, so the gateway needs no CORS and the browser never
// holds a URL it cannot reach. Only the gateway is used: no database or broker access (docs/phases/phase-12-admin-ui.md).
export const api = createClient<paths>({ baseUrl: "/gw" });

api.use({
  onRequest({ request }) {
    const token = getToken();
    if (token) request.headers.set("Authorization", `Bearer ${token}`);
    return request;
  },
});

export type Tenant = components["schemas"]["Tenant"];
export type Invoice = components["schemas"]["Invoice"];
export type InvoiceLine = components["schemas"]["InvoiceLine"];
export type InvoiceSaga = components["schemas"]["InvoiceSaga"];
export type SagaEvent = components["schemas"]["SagaEvent"];

/** The message of the gateway's error envelope, or a generic one. */
export function errorMessage(error: unknown): string {
  if (error && typeof error === "object" && "error" in error) {
    const e = (error as { error?: { code?: string; message?: string } }).error;
    if (e?.message) return `${e.code ?? "ERROR"}: ${e.message}`;
  }
  return error instanceof Error ? error.message : "request failed";
}
