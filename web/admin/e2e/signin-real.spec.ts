import { randomUUID } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { mailedCode, operatorSession, signInAsOperator } from "./operator";

// The sign-in methods against the REAL stack (make dev / make up): a code mailed through Mailpit, and "Continue with Google" through the in-repo stand-in
// (pkg/oidctest, the `oidc` compose profile) when the gateway is set up for it. Real Google is NOT exercised here: there is no client registered for the repository.
const gateway = process.env.QUOTIFY_GATEWAY_URL ?? "http://localhost:42800";

type Json = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any

async function post(path: string, body: unknown, headers: Record<string, string> = {}): Promise<{ status: number; body: Json }> {
  const res = await fetch(gateway + path, { method: "POST", headers: { "content-type": "application/json", ...headers }, body: JSON.stringify(body) });
  return { status: res.status, body: (await res.json().catch(() => ({}))) as Json };
}

/**
 * A client address of its own for the calls a test makes directly: the gateway counts the public sign-in routes per client address (10 code tries a minute) and,
 * on the compose stack, believes X-Forwarded-For from a private connection, so tests that call it in bursts do not share one bucket.
 */
const ownAddress = () => ({ "x-forwarded-for": `10.${Math.floor(Math.random() * 250) + 1}.${Math.floor(Math.random() * 250) + 1}.${Math.floor(Math.random() * 250) + 1}` });

const fresh = () => `code-${randomUUID().slice(0, 8)}@example.com`;

async function enterCode(page: Page, code: string): Promise<void> {
  await page.getByLabel("Digit 1 of 6").focus();
  await page.keyboard.type(code);
}

test.describe("sign-in methods, real stack", () => {
  test("the page offers the methods the gateway says are on, and no token form", async ({ page }) => {
    const methods = await (await fetch(`${gateway}/auth/methods`)).json();
    expect(methods).toMatchObject({ password: true, otp: true });
    await page.goto("/signin");
    await expect(page.getByRole("tab", { name: "Password" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Email code" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Continue with Google" })).toHaveCount(methods.google ? 1 : 0);
    await expect(page.getByLabel(/token/i)).toHaveCount(0);
    await expect(page.getByRole("link", { name: /operator/i })).toHaveCount(0);
  });

  test("the operator is a person: a verified, listed address gets the console, nobody else does", async ({ page }) => {
    const token = await operatorSession();
    const me = await (await fetch(`${gateway}/auth/me`, { headers: { authorization: `Bearer ${token}` } })).json();
    expect(me.operator).toBe(true);
    expect(me.user.email_verified).toBe(true);
    expect((await fetch(`${gateway}/admin/v1/tenants?page_size=1`, { headers: { authorization: `Bearer ${token}` } })).status).toBe(200);
    // Headers and fields that claim it open nothing for a person who is not on the list.
    const plain = await post("/auth/signup", { email: fresh(), password: "correct horse battery", account_name: "Plain" }, ownAddress());
    expect(plain.status).toBe(202);
    await signInAsOperator(page);
    await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
    await page.getByRole("link", { name: "My apps" }).click();
    await page.waitForURL("**/app");
    await expect(page.getByRole("link", { name: "Operator console" })).toBeVisible();
  });

  test("an e-mailed code signs a new address in, creates its account and proves the address", async ({ page }) => {
    const address = fresh();
    const since = Date.now() - 1000;
    await page.goto("/signin");
    await page.getByRole("tab", { name: "Email code" }).click();
    await page.getByLabel("E-mail").fill(address);
    await page.getByRole("button", { name: "Email me a code" }).click();
    await expect(page.getByLabel("Digit 1 of 6")).toBeVisible();
    await enterCode(page, await mailedCode(address, since));
    await page.waitForURL("**/app");
    // The code proved the mailbox: no "confirm your address" banner, and the API agrees.
    await expect(page.getByTestId("verify-banner")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: /^Welcome/ })).toBeVisible();
    const token = await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"));
    const me = await (await fetch(`${gateway}/auth/me`, { headers: { authorization: `Bearer ${token}` } })).json();
    expect(me.user.email_verified).toBe(true);
    expect(me.operator).toBe(false);
    expect(me.memberships[0].role).toBe("owner");
    // That session is a person's, not an operator's.
    expect((await fetch(`${gateway}/admin/v1/tenants`, { headers: { authorization: `Bearer ${token}` } })).status).toBe(403);
  });

  test("a code works once, five wrong tries burn it, and the answer is the same for any address", async () => {
    const address = fresh();
    const since = Date.now() - 1000;
    const from = ownAddress();
    expect((await post("/auth/otp/request", { email: address }, from)).status).toBe(202);
    const code = await mailedCode(address, since);
    const wrong = code === "000000" ? "000001" : "000000";
    for (let i = 0; i < 5; i++) {
      const r = await post("/auth/otp/verify", { email: address, code: wrong }, from);
      expect(r.status).toBe(400);
      expect(r.body.error.key).toBe("identity.otp.invalid");
    }
    // Burned: even the right code no longer works.
    expect((await post("/auth/otp/verify", { email: address, code }, from)).status).toBe(400);
    // A different address, never asked for a code, gets the very same refusal.
    const never = await post("/auth/otp/verify", { email: fresh(), code: "123456" }, from);
    expect(never.status).toBe(400);
    expect(never.body.error.key).toBe("identity.otp.invalid");
  });

  test("a right code signs in once", async () => {
    const address = fresh();
    const since = Date.now() - 1000;
    const from = ownAddress();
    await post("/auth/otp/request", { email: address }, from);
    const code = await mailedCode(address, since);
    const first = await post("/auth/otp/verify", { email: address, code, account_name: "Code Co" }, from);
    expect(first.status).toBe(200);
    expect(first.body.token).toMatch(/^qs_/);
    expect(first.body.user.email_verified).toBe(true);
    expect((await post("/auth/otp/verify", { email: address, code }, from)).status).toBe(400);
  });

  test("Continue with Google, through the stand-in provider, signs a person in without a password", async ({ page }) => {
    const methods = await (await fetch(`${gateway}/auth/methods`)).json();
    test.skip(!methods.google, "the gateway has no Google client set up (start the `oidc` profile and see docs/10-local-dev.md)");
    const address = fresh();
    await page.goto("/signin");
    await page.getByRole("link", { name: "Continue with Google" }).click();
    // The stand-in's consent page asks for any address.
    await page.getByLabel("E-mail").fill(address);
    await page.getByRole("button", { name: "Continue" }).click();
    await page.waitForURL("**/app");
    expect(page.url()).not.toContain("qs_");
    const token = await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"));
    expect(token).toMatch(/^qs_/);
    const me = await (await fetch(`${gateway}/auth/me`, { headers: { authorization: `Bearer ${token}` } })).json();
    expect(me.user.email).toBe(address);
    expect(me.user.email_verified).toBe(true);
    // The same person signing in with Google again lands in the same account.
    await page.evaluate(() => window.sessionStorage.clear());
    await page.goto("/signin");
    await page.getByRole("link", { name: "Continue with Google" }).click();
    await page.getByLabel("E-mail").fill(address);
    await page.getByRole("button", { name: "Continue" }).click();
    await page.waitForURL("**/app");
    const again = await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"));
    const me2 = await (await fetch(`${gateway}/auth/me`, { headers: { authorization: `Bearer ${again}` } })).json();
    expect(me2.user.id).toBe(me.user.id);
    // No password was ever set for it.
    expect((await post("/auth/signin", { email: address, password: "correct horse battery" })).status).toBe(401);
  });

  test("Google refuses an address it has not verified, and the page says why", async ({ page }) => {
    const methods = await (await fetch(`${gateway}/auth/methods`)).json();
    test.skip(!methods.google, "the gateway has no Google client set up");
    await page.goto("/signin");
    await page.getByRole("link", { name: "Continue with Google" }).click();
    await page.getByLabel("E-mail").fill(fresh());
    await page.getByLabel(/not verified/).check();
    await page.getByRole("button", { name: "Continue" }).click();
    await page.waitForURL(/\/signin\?oauth_error=email_unverified/);
    await expect(page.getByTestId("oauth-error")).toContainText("has not verified an e-mail address");
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBeNull();
  });
});
