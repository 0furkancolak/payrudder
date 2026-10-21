import type { Page, Route } from "@playwright/test";

export const TENANT = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e";
export const OPERATOR = "test-operator-token-0123456789abcdef";

export interface Call {
  method: string;
  path: string;
  query: URLSearchParams;
  headers: Record<string, string>;
  body: Record<string, unknown> | null;
}

type Handler = (call: Call) => { status?: number; body?: unknown } | undefined;

/**
 * A stand-in gateway: answers /gw requests from handlers the test provides (the first to return something wins), refuses what nobody
 * answers, and keeps every call so a test can check what the console sent.
 */
export class MockGateway {
  readonly calls: Call[] = [];
  private handlers: Handler[] = [];

  on(handler: Handler): this {
    this.handlers.unshift(handler);
    return this;
  }

  /** The calls to a method and path (a string, or a pattern for paths with ids in them). */
  called(method: string, path: string | RegExp): Call[] {
    return this.calls.filter((c) => c.method === method && (typeof path === "string" ? c.path === path : path.test(c.path)));
  }

  async install(page: Page): Promise<void> {
    await page.route("**/gw/**", async (route: Route) => {
      const req = route.request();
      const url = new URL(req.url());
      let body: Record<string, unknown> | null = null;
      try {
        body = req.postDataJSON() as Record<string, unknown> | null;
      } catch {
        body = null;
      }
      const call: Call = { method: req.method(), path: url.pathname.replace(/^\/gw/, ""), query: url.searchParams, headers: req.headers(), body };
      this.calls.push(call);
      for (const h of this.handlers) {
        const r = h(call);
        if (r) {
          await route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.body ?? {}) });
          return;
        }
      }
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "NOT_FOUND", message: `nobody mocked ${call.method} ${call.path}` } }) });
    });
  }
}

/** The calls every tenant page makes: the tenant itself and its (empty) invoices. */
export function baseTenant(gw: MockGateway, overrides: Record<string, unknown> = {}): MockGateway {
  return gw.on((c) => {
    if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}`) {
      return { body: { id: TENANT, name: "Acme", environment: "ENVIRONMENT_LIVE", tier: "TENANT_TIER_STANDARD", currency: "TRY", timezone: "Europe/Istanbul", grace_period: "24h", ...overrides } };
    }
    if (c.method === "GET" && c.path === "/admin/v1/tenants") return { body: { tenants: [] } };
    if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/invoices`) return { body: { invoices: [] } };
    return undefined;
  });
}

/** Signs in with the operator token and opens a page of the tenant. */
export async function openTenantPage(page: Page, path: string): Promise<void> {
  await page.goto("/operator");
  await page.getByLabel("Operator token").fill(OPERATOR);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/tenants");
  await page.goto(`/tenants/${TENANT}${path}`);
}
