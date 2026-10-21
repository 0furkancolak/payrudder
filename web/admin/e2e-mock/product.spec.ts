import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, LIVE, SESSION, overview, productGateway, signInAsAda, user } from "./product";

// The end-user product: sign up and in, the organisation home, an app's dashboard, API keys. The gateway is mocked; SHOTS=<dir> also writes screenshots
// of the pages (desktop and phone, light and dark) so the look can be reviewed.
const shots = process.env.SHOTS;
async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

test.describe("sign up and sign in", () => {
  test("a visitor signs up, lands on an empty organisation and creates the first app", async ({ page }) => {
    const gw = productGateway(new MockGateway(), { apps: [] }).gw;
    await gw.install(page);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
    await shoot(page, "01-signin");
    await page.getByRole("link", { name: "Create an account" }).click();
    await expect(page.getByRole("heading", { name: "Create your account" })).toBeVisible();

    await page.getByLabel("Your name").fill("Ada Lovelace");
    await page.getByLabel("Organisation").fill("Ada Labs");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("short");
    await expect(page.getByRole("button", { name: "Create account" })).toBeDisabled();
    await page.getByLabel("Password", { exact: true }).fill("a long enough password");
    await shoot(page, "02-signup");
    await page.getByRole("button", { name: "Create account" }).click();

    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/signup")[0]?.body).toEqual({ email: "ada@example.com", password: "a long enough password", display_name: "Ada Lovelace", account_name: "Ada Labs", locale: "en" });
    // Sign-up opens no session; the page signed in with the password just chosen.
    expect(gw.called("POST", "/auth/signin")[0]?.body).toEqual({ email: "ada@example.com", password: "a long enough password" });
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await shoot(page, "03-empty-home");

    await page.getByRole("button", { name: "Create app" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("App name").fill("Docs bot");
    await dialog.getByLabel("Currency").selectOption("EUR");
    await shoot(page, "04-create-app");
    await dialog.getByRole("button", { name: "Create app" }).click();
    await page.waitForURL(/\/app\/apps\//);
    expect(gw.called("POST", "/app/v1/apps")[0]?.body).toMatchObject({ name: "Docs bot", currency: "EUR" });
    // Every product call carries the session token and nothing else as credential.
    expect(gw.called("GET", "/app/v1/apps").every((c) => c.headers.authorization === `Bearer ${SESSION}`)).toBe(true);
  });

  test("a wrong password says so in words; the right one signs in", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("wrong password");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("alert")).toContainText("Wrong e-mail or password");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app");
    await expect(page.getByRole("heading", { name: "Welcome, Ada" })).toBeVisible();
  });

  test("the password can be shown and hidden", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    const field = page.getByLabel("Password", { exact: true });
    await field.fill("secret value");
    await expect(field).toHaveAttribute("type", "password");
    await page.getByRole("button", { name: "Show password" }).click();
    await expect(field).toHaveAttribute("type", "text");
  });

  test("a session the gateway no longer accepts sends the person back to sign in, with a reason", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await expect(page.getByRole("link", { name: /Web shop/ })).toBeVisible();
    state.sessionOk = false;
    await page.reload();
    await page.waitForURL(/\/signin/);
    await expect(page.getByRole("status")).toContainText("Your session has ended");
  });

  test("somebody who is not signed in is sent away from the app, and the operator keeps their own door", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/app");
    await page.waitForURL("**/signin");
    await page.goto("/tenants");
    await page.waitForURL("**/operator");
    await expect(page.getByLabel("Operator token")).toBeVisible();
  });
});

test.describe("organisation home", () => {
  test("an app card shows its status, thirty days of money per currency and what needs a look", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    const card = page.getByRole("link", { name: "Web shop" });
    await expect(card).toContainText("Needs attention");
    await expect(card).toContainText("$12,840.50");
    await expect(card).toContainText("TRY 18,500.00");
    await expect(card).toContainText("Failed payments: 9");
    await shoot(page, "05-home");
    await card.click();
    await page.waitForURL(`**/app/apps/${APP}`);
  });
});

test.describe("app dashboard", () => {
  test("shows the numbers of the live environment, per currency, with statuses as words", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("heading", { name: "Web shop" })).toBeVisible();
    await expect(page.getByTestId("kpi-revenue")).toHaveText("$12,840.50");
    await expect(page.getByTestId("kpi-payments")).toHaveText("146");
    await expect(page.getByTestId("kpi-open")).toHaveText("12");
    await expect(page.getByTestId("kpi-customers")).toHaveText("214");
    // The donut is a picture of numbers that are also listed.
    await expect(page.getByRole("img", { name: "146 payments by status" })).toBeVisible();
    const donut = page.getByRole("region", { name: "Payments by status" });
    await expect(donut).toContainText("Paid");
    await expect(donut).toContainText("128");
    await expect(donut).toContainText("Failed");
    // Currencies are never added together: each has its own bar.
    const currencies = page.getByRole("region", { name: "Revenue by currency" });
    await expect(currencies).toContainText("TRY");
    await expect(currencies).toContainText("USD");
    // The payments table.
    const payments = page.getByRole("region", { name: "Recent payments" });
    await expect(payments.getByRole("row")).toHaveCount(6);
    await expect(payments.getByRole("row", { name: /Failed/ })).toContainText("$150.00");
    await expect(payments.getByRole("row", { name: /Refunded/ })).toContainText("-$49.00");
    // Health.
    const health = page.getByRole("region", { name: "Health" });
    await expect(health).toContainText("2 failed deliveries");
    await expect(health).toContainText("iyzico");
    await expect(gw.called("GET", `/app/v1/apps/${APP}/overview`)[0]?.query.get("environment")).toBe("live");
    await shoot(page, "06-dashboard");
  });

  test("the switcher shows the sandbox, which says it is not real money", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("note")).toHaveCount(0);
    await page.getByRole("radio", { name: "Sandbox" }).check({ force: true });
    await expect(page.getByRole("note")).toContainText("Nothing here is real money");
    await expect(page).toHaveURL(/env=sandbox/);
    await expect.poll(() => gw.called("GET", `/app/v1/apps/${APP}/overview`).some((c) => c.query.get("environment") === "sandbox")).toBe(true);
  });

  test("a section that did not answer is named and the rest stays", async ({ page }) => {
    const down = overview("live", { status: "degraded", unavailable: ["payments"], payments: null });
    await productGateway(new MockGateway(), { overview: down }).gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("status").filter({ hasText: "did not answer" })).toContainText("payments");
    await expect(page.getByTestId("kpi-open")).toHaveText("12");
    await expect(page.getByRole("region", { name: "Recent payments" })).toContainText("Not available right now");
  });

  test("an API key is shown once, can be copied, and a revoked one is struck out", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    const keys = page.getByRole("region", { name: "API keys" });
    await expect(keys).toContainText("qk_live_abcd1234_••••••••");
    await expect(keys).not.toContainText("SECRET");

    await keys.getByRole("button", { name: "Live key" }).click();
    const reveal = page.getByRole("dialog");
    await expect(reveal.getByTestId("new-key")).toHaveText("qk_live_newkey99_SECRETSECRETSECRETSECRET");
    await shoot(page, "07-new-key");
    await reveal.getByRole("button", { name: "Copy" }).click();
    await expect(reveal.getByRole("button", { name: "Copied" })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("qk_live_newkey99_SECRETSECRETSECRETSECRET");
    expect(gw.called("POST", `/app/v1/apps/${APP}/api-keys`)[0]?.body).toEqual({ environment: "live" });
    await reveal.getByRole("button", { name: "I have saved it" }).click();
    // The secret is gone for good: the list holds the prefix only.
    await expect(page.getByText("SECRETSECRET")).toHaveCount(0);
    await expect(keys).toContainText("qk_live_newkey99_••••••••");

    await keys.getByRole("button", { name: "Revoke key qk_live_newkey99_••••••••" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
    await expect(keys.getByRole("listitem").filter({ hasText: "newkey99" })).toContainText("Revoked");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}/api-keys/k9`)).toHaveLength(1);
  });

  test("the deeper views are the console's own pages, spoken to as the person's tenant", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    gw.on((c) => (c.path === "/v1/customers" ? { body: { customers: [{ id: "c1", external_id: "cus_1", name: "Acme Ltd", email: "billing@acme.test" }] } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await page.getByRole("link", { name: /^Customers/ }).click();
    await page.waitForURL(`**/app/t/${LIVE}/customers`);
    await expect(page.getByRole("cell", { name: "Acme Ltd" })).toBeVisible();
    const call = gw.called("GET", "/v1/customers")[0];
    expect(call?.headers.authorization).toBe(`Bearer ${SESSION}`);
    expect(call?.headers["x-quotify-tenant"]).toBe(LIVE);
    await expect(page.getByRole("link", { name: /Web shop/ }).first()).toBeVisible();
    await shoot(page, "08-customers");
  });
});

test.describe("settings and chrome", () => {
  test("settings list the organisation, its members and the session; signing out forgets the token", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.getByRole("link", { name: "Settings" }).click();
    await expect(page.getByTestId("account-name")).toHaveText("Ada Labs");
    await expect(page.getByRole("region", { name: "Members" })).toContainText(user.email);
    await shoot(page, "09-settings");
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL("**/signin");
    expect(gw.called("POST", "/auth/signout")).toHaveLength(1);
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBeNull();
    await page.goto("/app");
    await page.waitForURL("**/signin");
  });

  test("dark mode applies to the whole app", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("heading", { name: "Web shop" })).toBeVisible();
    await page.getByRole("button", { name: "Switch to dark mode" }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await page.waitForTimeout(400); // let the colour transitions finish before the picture
    await shoot(page, "10-dashboard-dark");
    await page.getByRole("button", { name: "Switch to light mode" }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  });

  test("the session token lives in this tab only", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await signInAsAda(page);
    const stored = await page.evaluate(() => ({ session: window.sessionStorage.getItem("quotify.sessionToken"), local: Object.keys(window.localStorage) }));
    expect(stored.session).toBe(SESSION);
    expect(stored.local.filter((k) => /token|session/i.test(k))).toEqual([]);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the rail becomes a bar, nothing scrolls sideways, and the dashboard reads in one column", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/");
    await shoot(page, "11-signin-phone");
    await signInAsAda(page);
    await shoot(page, "12-home-phone");
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByTestId("kpi-revenue")).toBeVisible();
    await shoot(page, "13-dashboard-phone");
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const rail = page.getByRole("navigation", { name: "Quotify" });
    const box = await rail.boundingBox();
    expect(box && box.y > 700).toBe(true);
  });
});

test.describe("e-mail verification and password reset", () => {
  test("a sign-up whose address is taken shows the same notice as ever and opens no session", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signup");
    await page.getByLabel("Organisation").fill("Ada Labs");
    await page.getByLabel("E-mail").fill("ada@example.com");
    // The address exists with another password: the sign-in right after the sign-up fails, and the page says to check the inbox.
    await page.getByLabel("Password", { exact: true }).fill("another long password");
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page.getByRole("status")).toContainText("Check your inbox");
    await expect(page.getByRole("status")).toContainText("ada@example.com");
    expect(page.url()).toContain("/signup");
    expect(await page.evaluate(() => window.sessionStorage.getItem("quotify.sessionToken"))).toBeNull();
  });

  test("forgot password: the same answer for any address, then the reset page sets a new password", async ({ page }) => {
    const gw = productGateway(new MockGateway()).gw;
    await gw.install(page);
    await page.goto("/signin");
    await page.getByRole("link", { name: "Forgot your password?" }).click();
    await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
    await page.getByLabel("E-mail").fill("nobody@example.com");
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByRole("status")).toContainText("If an account exists for nobody@example.com");
    expect(gw.called("POST", "/auth/forgot")[0]?.body).toEqual({ email: "nobody@example.com", locale: "en" });

    // The link of the mail: the token is in the fragment, is read once and leaves the address bar.
    await page.goto("/reset#token=qr_good");
    await expect(page.getByRole("heading", { name: "Choose a new password" })).toBeVisible();
    expect(page.url()).not.toContain("qr_good");
    await page.getByLabel("New password", { exact: true }).fill("short");
    await expect(page.getByRole("button", { name: "Save password" })).toBeDisabled();
    await page.getByLabel("New password", { exact: true }).fill("a brand new password");
    await page.getByRole("button", { name: "Save password" }).click();
    await expect(page.getByRole("status")).toContainText("Password changed");
    expect(gw.called("POST", "/auth/reset")[0]?.body).toEqual({ token: "qr_good", password: "a brand new password" });
    await page.getByRole("link", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  });

  test("a used reset link and a page opened without a link say what to do", async ({ page }) => {
    await productGateway(new MockGateway(), { resetToken: "" }).gw.install(page);
    await page.goto("/reset#token=qr_good");
    await page.getByLabel("New password", { exact: true }).fill("a brand new password");
    await page.getByRole("button", { name: "Save password" }).click();
    await expect(page.getByRole("alert")).toContainText("invalid, has expired or was already used");
    await expect(page.getByRole("link", { name: "Ask for a new link" })).toBeVisible();
    await page.goto("/reset");
    await expect(page.getByRole("alert")).toContainText("needs the link from your e-mail");
  });

  test("an unverified person sees the banner, can resend the link and cannot make a live key until the link is followed", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { verified: false });
    await gw.install(page);
    await signInAsAda(page);
    const banner = page.getByTestId("verify-banner");
    await expect(banner).toContainText("Confirm your e-mail address (ada@example.com)");
    await banner.getByRole("button", { name: "Send the link again" }).click();
    await expect(banner).toContainText("Link sent");
    expect(gw.called("POST", "/auth/resend-verification")).toHaveLength(1);

    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByRole("button", { name: "Live key" })).toBeDisabled();
    await expect(page.getByTestId("live-needs-verified")).toBeVisible();
    await expect(page.getByRole("button", { name: "Sandbox key" })).toBeEnabled();

    // The link of the mail, followed in this browser.
    await page.goto("/verify#token=qv_good");
    await expect(page.getByRole("status")).toContainText("E-mail address confirmed");
    expect(page.url()).not.toContain("qv_good");
    expect(gw.called("POST", "/auth/verify")[0]?.body).toEqual({ token: "qv_good" });
    await page.goto(`/app/apps/${APP}`);
    await expect(page.getByTestId("verify-banner")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Live key" })).toBeEnabled();
  });

  test("a verification link that was used is explained, signed out too", async ({ page }) => {
    await productGateway(new MockGateway(), { verifyToken: "" }).gw.install(page);
    await page.goto("/verify#token=qv_old");
    await expect(page.getByRole("alert")).toContainText("This link does not work");
    await expect(page.getByRole("link", { name: "Sign in" })).toBeVisible();
  });
});
