import { expect, test, type Page } from "@playwright/test";
import { MockGateway, OPERATOR, OPERATOR_EMAIL, OPERATOR_PASSWORD, signInAsOperator } from "./gateway";
import { SESSION, productGateway } from "./product";
import { pickLanguage } from "./ui";

// The three ways to sign in (phase 42): e-mail and password, a one-time code mailed to the address, and Google. The gateway is mocked. SHOTS=<dir> also
// writes screenshots of the pages so the look can be reviewed.
const shots = process.env.SHOTS;
async function shoot(page: Page, name: string): Promise<void> {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

const sessionToken = (page: Page) => page.evaluate(() => window.sessionStorage.getItem("payrudder.sessionToken"));

test.describe("the sign-in page", () => {
  test("offers a password, an e-mailed code and, when the deployment has it, Google", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { methods: { password: true, otp: true, google: true } });
    await gw.install(page);
    await page.goto("/signin");
    await expect(page.getByRole("tab", { name: "Password" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("tab", { name: "Email code" })).toBeVisible();
    const google = page.getByRole("link", { name: "Continue with Google" });
    await expect(google).toBeVisible();
    await expect(google).toHaveAttribute("href", "/gw/auth/google/start?locale=en");
    await shoot(page, "10-signin-methods");
    // Nothing else signs anybody in: no token form, no operator door.
    await expect(page.getByLabel(/token/i)).toHaveCount(0);
    await expect(page.getByRole("link", { name: /operator/i })).toHaveCount(0);
  });

  test("hides the Google button when the deployment has not set it up", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    await expect(page.getByRole("tab", { name: "Email code" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Continue with Google" })).toHaveCount(0);
  });

  test("the Google button speaks the page's language", async ({ page }) => {
    await productGateway(new MockGateway(), { methods: { password: true, otp: true, google: true } }).gw.install(page);
    await page.goto("/signin");
    await pickLanguage(page, "de");
    const google = page.getByRole("link", { name: "Weiter mit Google" });
    await expect(google).toHaveAttribute("href", "/gw/auth/google/start?locale=de");
    // The button keeps Google's own look: a white face and the multicolour mark (light theme).
    await expect(google).toHaveCSS("background-color", "rgb(255, 255, 255)");
    await expect(google.locator("svg path")).toHaveCount(4);
  });

  test("a sign-in with Google that ended without a session says why, in words", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin?oauth_error=denied");
    await expect(page.getByTestId("oauth-error")).toContainText("cancelled");
    await page.goto("/signin?oauth_error=email_unverified");
    await expect(page.getByTestId("oauth-error")).toContainText("has not verified an e-mail address");
    // Only the fixed reasons are shown; anything else is ignored.
    await page.goto("/signin?oauth_error=%3Cscript%3E");
    await expect(page.getByTestId("oauth-error")).toHaveCount(0);
  });

  test("coming back from Google with a session token signs in, removes the token from the address bar and opens the app", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await page.goto(`/signin/complete#token=${SESSION}&expires=2026-11-04T09:00:00Z`);
    await page.waitForURL("**/app");
    expect(page.url()).not.toContain("qs_");
    expect(await sessionToken(page)).toBe(SESSION);
    expect(gw.called("GET", "/auth/me")[0]?.headers.authorization).toBe(`Bearer ${SESSION}`);
  });

  test("coming back without a token goes to the sign-in page with a reason", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin/complete");
    await page.waitForURL(/\/signin\?oauth_error=failed/);
    await expect(page.getByTestId("oauth-error")).toBeVisible();
    await page.goto("/signin/complete#token=not-a-session");
    await page.waitForURL(/\/signin\?oauth_error=failed/);
    expect(await sessionToken(page)).toBeNull();
  });
});

test.describe("sign in with an e-mailed code", () => {
  async function askForCode(page: Page) {
    await page.goto("/signin");
    await page.getByRole("tab", { name: "Email code" }).click();
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByRole("button", { name: "Email me a code" }).click();
    await expect(page.getByRole("status").filter({ hasText: "6-digit code is on its way" })).toBeVisible();
  }

  test("six boxes take the digits as they are typed and sign in at the sixth", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await askForCode(page);
    expect(gw.called("POST", "/auth/otp/request")[0]?.body).toEqual({ email: "ada@example.com", locale: "en" });
    await shoot(page, "11-code-entry");
    await page.getByLabel("Digit 1 of 6").focus();
    await page.keyboard.type("123456");
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/otp/verify")[0]?.body).toEqual({ email: "ada@example.com", code: "123456", locale: "en" });
    expect(await sessionToken(page)).toBe(SESSION);
  });

  test("a pasted code fills all six boxes, spaces and all", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await askForCode(page);
    await page.getByLabel("Digit 3 of 6").evaluate((el) => {
      const data = new DataTransfer();
      data.setData("text", "123 456");
      el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
    });
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/otp/verify")[0]?.body?.code).toBe("123456");
  });

  test("a wrong code is said in words, the boxes empty and the person can try again", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await askForCode(page);
    await page.getByLabel("Digit 1 of 6").focus();
    await page.keyboard.type("000000");
    await expect(page.getByRole("alert")).toContainText("wrong, has expired or was already used");
    await expect(page.getByLabel("Digit 1 of 6")).toHaveValue("");
    await expect(page.getByLabel("Digit 1 of 6")).toBeFocused();
    await page.keyboard.type("123456");
    await page.waitForURL("**/app");
  });

  test("Backspace goes back a box and the arrow keys move", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await askForCode(page);
    await page.getByLabel("Digit 1 of 6").focus();
    await page.keyboard.type("12");
    await expect(page.getByLabel("Digit 3 of 6")).toBeFocused();
    await page.keyboard.press("Backspace");
    await expect(page.getByLabel("Digit 2 of 6")).toBeFocused();
    await expect(page.getByLabel("Digit 2 of 6")).toHaveValue("");
    await page.keyboard.press("ArrowLeft");
    await expect(page.getByLabel("Digit 1 of 6")).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByLabel("Digit 2 of 6")).toBeFocused();
  });

  test("a new code can be asked for once the countdown has run out", async ({ page }) => {
    await page.clock.install();
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await askForCode(page);
    const resend = page.getByRole("button", { name: /Send a new code/ });
    await expect(resend).toBeDisabled();
    await expect(resend).toContainText("60 s");
    await page.clock.runFor(30_000);
    await expect(resend).toContainText("30 s");
    await page.clock.runFor(31_000);
    await expect(resend).toBeEnabled();
    await expect(resend).toHaveText("Send a new code");
    await resend.click();
    await expect(page.getByText("A new code is on its way.")).toBeVisible();
    expect(gw.called("POST", "/auth/otp/request")).toHaveLength(2);
    await expect(page.getByRole("button", { name: /Send a new code/ })).toBeDisabled();
  });

  test("another address can be chosen", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await askForCode(page);
    await page.getByRole("button", { name: "Use a different address" }).click();
    await expect(page.getByLabel("E-mail")).toHaveValue("ada@example.com");
    await expect(page.getByRole("button", { name: "Email me a code" })).toBeVisible();
  });

  test("the code boxes stay left to right in Arabic, and the page is right to left", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await page.getByRole("tab", { name: "رمز بالبريد" }).click();
    await page.getByLabel("البريد الإلكتروني").fill("ada@example.com");
    await page.getByRole("button", { name: "أرسل لي رمزًا" }).click();
    await expect(page.getByRole("group", { name: "رمز من 6 أرقام" })).toHaveAttribute("dir", "ltr");
    await shoot(page, "12-code-entry-ar");
  });

  test("fits a phone without sideways scrolling", async ({ page }) => {
    await productGateway(new MockGateway(), { methods: { password: true, otp: true, google: true } }).gw.install(page);
    await page.setViewportSize({ width: 360, height: 740 });
    await askForCode(page);
    const o = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
    expect(o.sw).toBeLessThanOrEqual(o.cw);
    await shoot(page, "13-code-entry-phone");
  });
});

test.describe("sign up with the same methods", () => {
  test("a code creates the account: name and organisation travel with the code, nothing is signed in before it", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { methods: { password: true, otp: true, google: true } });
    await gw.install(page);
    await page.goto("/signup");
    await expect(page.getByRole("link", { name: "Continue with Google" })).toBeVisible();
    await page.getByRole("tab", { name: "Email code" }).click();
    await page.getByLabel("Your name").fill("Ned New");
    await page.getByLabel("Organisation (optional)").fill("Ned Labs");
    await page.getByLabel("E-mail").fill("ned@example.com");
    await page.getByRole("button", { name: "Email me a code" }).click();
    expect(await sessionToken(page)).toBeNull();
    await page.getByLabel("Digit 1 of 6").focus();
    await page.keyboard.type("123456");
    await page.waitForURL("**/app");
    expect(gw.called("POST", "/auth/otp/verify")[0]?.body).toEqual({ email: "ned@example.com", code: "123456", display_name: "Ned New", account_name: "Ned Labs", locale: "en" });
    expect(gw.called("POST", "/auth/signup")).toHaveLength(0);
    await shoot(page, "14-signup-methods");
  });
});

test.describe("the operator is a person", () => {
  test("signs in like everybody else and finds the operator console in the shell; the machine token is not asked for", async ({ page }) => {
    const gw = new MockGateway().on((c) => (c.method === "GET" && c.path === "/admin/v1/tenants" ? { body: { tenants: [] } } : undefined));
    await gw.install(page);
    await signInAsOperator(page);
    await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
    expect(gw.called("POST", "/auth/signin")[0]?.body).toEqual({ email: OPERATOR_EMAIL, password: OPERATOR_PASSWORD });
    // The operator routes are called with the session token, which is all this tab holds.
    expect(gw.called("GET", "/admin/v1/tenants")[0]?.headers.authorization).toBe(`Bearer ${OPERATOR}`);
    expect(await sessionToken(page)).toBe(OPERATOR);
    expect(await page.evaluate(() => Object.keys(window.sessionStorage))).not.toContain("payrudder.operatorToken");
    await shoot(page, "15-operator-console");
    // The rail leads back to the person's own apps.
    await page.getByRole("link", { name: "My apps" }).click();
    await page.waitForURL("**/app");
    await expect(page.getByRole("link", { name: "Operator console" })).toBeVisible();
    // No label is a raw translation key.
    await expect(page.locator("body")).not.toContainText("shell.");
    await shoot(page, "16-operator-door");
  });

  test("a person who is not an operator sees no door to it, and the address leads home", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app");
    await expect(page.getByRole("link", { name: "Operator console" })).toHaveCount(0);
    for (const path of ["/operator", "/tenants", "/playground", "/dead-letters"]) {
      await page.goto(path);
      await page.waitForURL("**/app");
    }
  });

  test("a claim in the browser's storage opens nothing: the server's verdict wins and the person is sent home", async ({ page }) => {
    const forbidden = { status: 403, body: { error: { code: "PERMISSION_DENIED", key: "identity.operator.forbidden", message: "operators only" } } };
    const gw = productGateway(new MockGateway()).gw.on((c) => (c.path.startsWith("/admin/v1") ? forbidden : undefined));
    await gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app");
    // A person edits their own storage to look like an operator and opens the console.
    await page.evaluate(() => window.sessionStorage.setItem("payrudder.operator", "1"));
    await page.goto("/tenants");
    // /auth/me says operator=false: the flag is corrected and the person is sent to their own home. They stay signed in (a 403 is not an expired session).
    await page.waitForURL("**/app");
    await expect(page.getByRole("heading", { name: "Tenants" })).toHaveCount(0);
    expect(await sessionToken(page)).toBe(SESSION);
    expect(await page.evaluate(() => window.sessionStorage.getItem("payrudder.operator"))).toBeNull();
    // Whatever the page asked of the operator routes got the server's refusal, carrying only the person's own session.
    expect(gw.called("GET", "/admin/v1/tenants").every((c) => c.headers.authorization === `Bearer ${SESSION}`)).toBe(true);
  });
});
