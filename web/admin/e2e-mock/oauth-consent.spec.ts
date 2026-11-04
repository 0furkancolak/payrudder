import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { SANDBOX, LIVE, productGateway } from "./product";

// An assistant sends the person to the console's consent page (docs/plans/20-mcp.md, "OAuth design"; default, not agreed): who is asking, where the browser goes back to,
// which environment, which scopes; the decision is sent to the gateway and the browser opens the address it answers with. The gateway is mocked.

const CLIENT = "https://client.example/meta.json";
const REDIRECT = "https://client.example/callback";
const REQUEST = {
  client_id: CLIENT,
  redirect_uri: REDIRECT,
  response_type: "code",
  code_challenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  code_challenge_method: "S256",
  scope: "customers:read customers:write invoices:read",
  resource: "https://mcp.example.com/mcp",
  state: "007",
};
const LINK = "/oauth/authorize?" + new URLSearchParams(REQUEST).toString();
const described = (over: Record<string, unknown> = {}) => ({
  client_name: "Example Assistant", client_id: CLIENT, client_host: "client.example", redirect_host: "client.example", redirect_loopback: false,
  resource: REQUEST.resource, scopes: ["customers:read", "customers:write", "invoices:read"], allow_live: false, ...over,
});

function gateway(over: Record<string, unknown> = {}, bad = false) {
  const { gw } = productGateway(new MockGateway());
  gw.on((c) => {
    if (c.method === "GET" && c.path === "/app/v1/oauth/request") {
      return bad
        ? { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "identity.oauth.bad_request", message: "redirect_uri is not one the client registered" } } }
        : { body: described(over) };
    }
    if (c.method === "POST" && c.path === "/app/v1/oauth/consent") {
      const u = new URL(REDIRECT);
      if (c.body?.decision === "deny") u.searchParams.set("error", "access_denied");
      else u.searchParams.set("code", "code-123");
      u.searchParams.set("state", String(c.body?.state));
      u.searchParams.set("iss", "https://api.example.com");
      return { body: { redirect_to: u.toString() } };
    }
    return undefined;
  });
  return gw;
}

async function open(page: Page, gw: MockGateway) {
  await gw.install(page);
  // The client's own address: whatever the browser is sent to is caught here, nothing leaves the test.
  await page.route("https://client.example/**", (r) => r.fulfill({ status: 200, contentType: "text/html", body: "<p>client</p>" }));
  await page.goto(LINK);
  // Signed out: the sign-in page first, and the request is still there afterwards.
  await page.getByLabel("E-mail").fill("ada@example.com");
  await page.getByLabel("Password", { exact: true }).fill("right password!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByTestId("oauth-consent")).toBeVisible();
}

test.describe("the consent page", () => {
  test("shows who asks and where the browser returns, a sandbox is ticked and a live environment cannot be chosen while the deployment has it off", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await expect(page.getByTestId("oauth-intro")).toContainText("Example Assistant");
    await expect(page.getByTestId("oauth-client-host")).toContainText("client.example");
    await expect(page.getByTestId("oauth-redirect-host")).toContainText("client.example");
    await expect(page.getByTestId("oauth-loopback")).toHaveCount(0);
    await expect(page.getByTestId("oauth-env-sandbox-Web shop")).toBeChecked();
    await expect(page.getByTestId("oauth-env-live-Web shop")).toBeDisabled();
    await expect(page.getByTestId("oauth-live-off")).toBeVisible();
    // The request reached the gateway as text, the numeric-looking state untouched.
    const asked = gw.called("GET", "/app/v1/oauth/request")[0];
    expect(asked?.query.get("state")).toBe("007");
    expect(asked?.query.get("client_id")).toBe(CLIENT);
    expect(asked?.query.get("code_challenge_method")).toBe("S256");
    expect(asked?.headers.authorization).toMatch(/^Bearer qs_/);
  });

  test("allow sends the environment and the scopes that stayed ticked, then the browser goes to the address the gateway answered with", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("oauth-scope-customers:write").click();
    await page.getByTestId("oauth-allow").click();
    await page.waitForURL(/client\.example\/callback/);
    const u = new URL(page.url());
    expect(u.searchParams.get("code")).toBe("code-123");
    expect(u.searchParams.get("state")).toBe("007");
    expect(u.searchParams.get("iss")).toBe("https://api.example.com");
    const sent = gw.called("POST", "/app/v1/oauth/consent")[0]?.body;
    expect(sent).toMatchObject({ decision: "allow", tenant_id: SANDBOX, client_id: CLIENT, redirect_uri: REDIRECT, state: "007", resource: REQUEST.resource });
    expect(sent?.scopes).toEqual(["customers:read", "invoices:read"]);
  });

  test("deny sends no environment and no scopes, and returns the error to the client", async ({ page }) => {
    const gw = gateway();
    await open(page, gw);
    await page.getByTestId("oauth-deny").click();
    await page.waitForURL(/client\.example\/callback/);
    expect(new URL(page.url()).searchParams.get("error")).toBe("access_denied");
    const sent = gw.called("POST", "/app/v1/oauth/consent")[0]?.body;
    expect(sent?.decision).toBe("deny");
    expect(sent).not.toHaveProperty("tenant_id");
    expect(sent).not.toHaveProperty("scopes");
  });

  test("allow needs at least one scope, and a live environment can be chosen when the deployment allows it", async ({ page }) => {
    const gw = gateway({ allow_live: true });
    await open(page, gw);
    await expect(page.getByTestId("oauth-env-live-Web shop")).toBeEnabled();
    await expect(page.getByTestId("oauth-live-off")).toHaveCount(0);
    for (const s of ["customers:read", "customers:write", "invoices:read"]) await page.getByTestId(`oauth-scope-${s}`).click();
    await expect(page.getByTestId("oauth-no-scopes")).toBeVisible();
    await expect(page.getByTestId("oauth-allow")).toBeDisabled();
    await page.getByTestId("oauth-scope-invoices:read").click();
    await page.getByTestId("oauth-env-live-Web shop").check();
    await page.getByTestId("oauth-allow").click();
    await page.waitForURL(/client\.example\/callback/);
    expect(gw.called("POST", "/app/v1/oauth/consent")[0]?.body).toMatchObject({ tenant_id: LIVE, scopes: ["invoices:read"] });
  });

  test("a redirect to the person's own machine carries a warning", async ({ page }) => {
    await open(page, gateway({ redirect_loopback: true, redirect_host: "localhost:3000" }));
    await expect(page.getByTestId("oauth-loopback")).toBeVisible();
    await expect(page.getByTestId("oauth-redirect-host")).toContainText("localhost:3000");
  });

  test("a request the gateway refuses is shown as an error with nothing to allow, and nobody is redirected", async ({ page }) => {
    const gw = gateway({}, true);
    await open(page, gw);
    await expect(page.getByTestId("oauth-invalid")).toContainText("This request cannot be used");
    await expect(page.getByTestId("oauth-allow")).toHaveCount(0);
    expect(page.url()).toContain("/oauth/authorize");
    expect(gw.called("POST", "/app/v1/oauth/consent")).toHaveLength(0);
  });
});

test("the security page lists the assistants that were let in and ends one", async ({ page }) => {
  const { gw } = productGateway(new MockGateway());
  let grants = [{ id: "9f000000-0000-4000-8000-000000000001", client_id: CLIENT, client_name: "Example Assistant", tenant_id: SANDBOX, app_name: "Web shop", environment: "ENVIRONMENT_SANDBOX", resource: REQUEST.resource, scopes: ["customers:read", "invoices:read"], create_time: "2026-11-01T09:00:00Z" }];
  gw.on((c) => {
    if (c.method === "GET" && c.path === "/app/v1/oauth/grants") return { body: { grants } };
    if (c.method === "DELETE" && c.path === `/app/v1/oauth/grants/${grants[0]?.id}`) {
      grants = [];
      return { status: 204, body: {} };
    }
    return undefined;
  });
  await gw.install(page);
  await page.goto("/");
  await page.getByLabel("E-mail").fill("ada@example.com");
  await page.getByLabel("Password", { exact: true }).fill("right password!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL(/\/app/);
  await page.goto("/app/security");
  const row = page.getByTestId("oauth-grant-9f000000-0000-4000-8000-000000000001");
  await expect(row).toContainText("Example Assistant");
  await expect(row).toContainText("Web shop, sandbox. 2 permissions.");
  await expect(row).toContainText("Not used yet");
  await page.getByTestId("oauth-revoke-9f000000-0000-4000-8000-000000000001").click();
  await expect(page.getByTestId("oauth-grants-empty")).toBeVisible();
  expect(gw.called("DELETE", "/app/v1/oauth/grants/9f000000-0000-4000-8000-000000000001")).toHaveLength(1);
});
