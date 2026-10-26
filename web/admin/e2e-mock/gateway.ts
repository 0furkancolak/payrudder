import type { Page, Route } from "@playwright/test";
import { dashboardFixture } from "./dashboard";
import { geoHandler } from "./geo";

export const TENANT = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e";
/** The session token of the operator user the console tests sign in as (a person whose address the gateway lists in PAYRUDDER_OPERATOR_EMAILS). */
export const OPERATOR = "qs_" + "O".repeat(43);
export const OPERATOR_EMAIL = "user1@example.com";
export const OPERATOR_PASSWORD = "password123";

/** The sign-in answer and the /auth/me answer of that operator user; `operator: true` is the server's verdict. */
export const operatorUser = { id: "99999999-9999-4999-8999-999999999999", email: OPERATOR_EMAIL, display_name: "Operator One", email_verified: true, create_time: "2026-10-01T09:00:00Z" };

export interface Call {
  method: string;
  path: string;
  query: URLSearchParams;
  headers: Record<string, string>;
  body: Record<string, unknown> | null;
}

/** An answer: a JSON body, or `raw` bytes with their own content type (a file). */
type Answer = { status?: number; body?: unknown; raw?: Buffer; contentType?: string };
type Handler = (call: Call) => Answer | undefined;

/**
 * The last resort of every mocked gateway: the operator user can sign in with a password and is recognised by /auth/me. A test that mocks its own people
 * (product.ts) answers first, so this only ever fills in for the console tests that just need an operator.
 */
const operatorSignIn: Handler = (c) => {
  if (c.method === "POST" && c.path === "/auth/signin" && c.body?.email === OPERATOR_EMAIL && c.body?.password === OPERATOR_PASSWORD) {
    return { body: { user: operatorUser, memberships: [], token: OPERATOR, operator: true, expire_time: "2026-11-04T09:00:00Z" } };
  }
  if (c.method === "GET" && c.path === "/auth/me" && c.headers["authorization"] === `Bearer ${OPERATOR}`) {
    return { body: { user: operatorUser, memberships: [], operator: true, expire_time: "2026-11-04T09:00:00Z" } };
  }
  // The operator is a person too, and the shell asks for their own organisation and apps (none here) whatever page they are on.
  if (c.method === "GET" && c.path === "/app/v1/apps") return { body: { apps: [] } };
  if (c.method === "GET" && c.path === "/app/v1/account") return { body: { id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", name: "Operator Co", role: "owner", members: [] } };
  return undefined;
};

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
      for (const h of [...this.handlers, operatorSignIn, geoHandler] as Handler[]) {
        const r = h(call);
        if (r) {
          if (r.raw) await route.fulfill({ status: r.status ?? 200, contentType: r.contentType ?? "application/octet-stream", body: r.raw });
          else await route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.body ?? {}) });
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
    if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/dashboard`) return { body: dashboardFixture(Object.fromEntries(c.query.entries())) };
    if (c.method === "GET" && c.path === "/admin/v1/tenants") return { body: { tenants: [] } };
    if (c.method === "GET" && c.path === `/admin/v1/tenants/${TENANT}/invoices`) return { body: { invoices: [] } };
    // The accounting page also reads the invoice inbox and the vendor rules; they are empty unless a test says otherwise.
    if (c.method === "GET" && c.path === "/v1/expense-inbox") return { body: { token: "abcdefghijklmnopqrstuvwxyz234567", address: "invoices+abcdefghijklmnopqrstuvwxyz234567@in.example.test" } };
    if (c.method === "GET" && c.path === "/v1/expense-inbox/items") return { body: { items: [] } };
    if (c.method === "GET" && c.path === "/v1/expense-rules") return { body: { rules: [] } };
    return undefined;
  });
}

/** Signs in as the operator user (e-mail and password, through the page) and opens the operator console's tenant list. */
export async function signInAsOperator(page: Page): Promise<void> {
  await page.goto("/signin");
  await page.getByLabel("E-mail").fill(OPERATOR_EMAIL);
  await page.getByLabel("Password", { exact: true }).fill(OPERATOR_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/app");
  await page.goto("/operator");
  await page.waitForURL("**/tenants");
}

/** Signs in as the operator user and opens a page of the tenant in the operator console. */
export async function openTenantPage(page: Page, path: string): Promise<void> {
  await signInAsOperator(page);
  await page.goto(`/tenants/${TENANT}${path}`);
}
