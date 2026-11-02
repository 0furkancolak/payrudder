import { expect, test, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { MockGateway } from "./gateway";
import { CHALLENGE, HOME, SESSION, TOTP_SECRET, productGateway, recoveryCodes, signInAsAda, type ProductState } from "./product";
import { pickLanguage } from "./ui";

// Two-factor sign-in (plan 22, G9): the second step of a sign-in with a password, an e-mailed code and Google, the security page (turn it on with a QR code and
// recovery codes, turn it off, new recovery codes), the organisation's policy and the page a person who must set it up is held on. The gateway is mocked.

const sessionToken = (page: Page) => page.evaluate(() => window.sessionStorage.getItem("payrudder.sessionToken"));
const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
const withTwoFactor = (over: Partial<ProductState["twoFactor"]> = {}): Partial<ProductState> => ({ twoFactor: { enabled: true, required: false, recovery: recoveryCodes("old"), started: false, code: "123456", enableTime: "2026-10-01T09:00:00Z", ...over } });

async function passwordSignIn(page: Page) {
  await page.goto("/signin");
  await page.getByLabel("E-mail").fill("ada@example.com");
  await page.getByLabel("Password", { exact: true }).fill("right password!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByTestId("two-factor-step")).toBeVisible();
}
const typeDigits = async (page: Page, digits: string) => {
  await page.getByLabel("Digit 1 of 6").focus();
  await page.keyboard.type(digits);
};

test.describe("the second step of a sign-in", () => {
  test("a password does not sign in a person with two-factor on: the step asks for the code, and the right one opens the session", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await passwordSignIn(page);
    // No session yet: nothing was stored, and the person is still on the sign-in page.
    expect(await sessionToken(page)).toBeNull();
    expect(page.url()).toContain("/signin");
    await expect(page.getByRole("button", { name: "Sign in" })).toHaveCount(0);
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    expect(await sessionToken(page)).toBe(SESSION);
    expect(gw.called("POST", "/auth/2fa/verify")[0]?.body).toEqual({ challenge: CHALLENGE, code: "123456" });
    expect(gw.called("GET", "/auth/me")[0]?.headers.authorization).toBe(`Bearer ${SESSION}`);
  });

  test("a wrong code stays on the step with the reason and empty boxes, and the right one then signs in", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "000000");
    await expect(page.getByRole("alert")).toContainText("Wrong or expired code.");
    await expect(page.getByTestId("two-factor-step")).toBeVisible();
    await expect(page.getByLabel("Digit 1 of 6")).toHaveValue("");
    await expect(page.getByLabel("Digit 1 of 6")).toBeFocused();
    expect(await sessionToken(page)).toBeNull();
    await page.keyboard.type("123456");
    await page.waitForURL(HOME);
    expect(gw.called("POST", "/auth/2fa/verify")).toHaveLength(2);
  });

  test("a recovery code works in its own box, once", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await passwordSignIn(page);
    await page.getByTestId("two-factor-switch").click();
    await expect(page.getByLabel("Digit 1 of 6")).toHaveCount(0);
    await page.getByLabel("Recovery code").fill("wrong-code");
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(page.getByRole("alert")).toContainText("Wrong or expired code.");
    await expect(page.getByLabel("Recovery code")).toHaveValue("");
    await page.getByLabel("Recovery code").fill("old3-abcd");
    await page.getByRole("button", { name: "Verify" }).click();
    await page.waitForURL(HOME);
    expect(gw.called("POST", "/auth/2fa/verify").at(-1)?.body).toEqual({ challenge: CHALLENGE, code: "old3-abcd" });
    expect(state.twoFactor.recovery).not.toContain("old3-abcd");
    expect(state.twoFactor.recovery).toHaveLength(9);
  });

  test("the app's code and the recovery box switch back and forth, and 'Start again' returns to the password", async ({ page }) => {
    await productGateway(new MockGateway(), withTwoFactor()).gw.install(page);
    await passwordSignIn(page);
    await page.getByTestId("two-factor-switch").click();
    await expect(page.getByLabel("Recovery code")).toBeVisible();
    await page.getByTestId("two-factor-switch").click();
    await expect(page.getByLabel("Digit 1 of 6")).toBeVisible();
    await page.getByTestId("two-factor-restart").click();
    await expect(page.getByTestId("two-factor-step")).toHaveCount(0);
    await expect(page.getByLabel("E-mail")).toHaveValue("ada@example.com");
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("a challenge that ended is refused like a wrong code, and starting again is the way out", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor());
    gw.on((c) => (c.path === "/auth/2fa/verify" ? { status: 401, body: { error: { code: "UNAUTHENTICATED", key: "identity.two_factor.invalid", message: "expired" } } } : undefined));
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "123456");
    await expect(page.getByRole("alert")).toContainText("Wrong or expired code.");
    await page.getByTestId("two-factor-restart").click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("an e-mailed code is the first step and the app's code the second", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await page.goto("/signin");
    await page.getByRole("tab", { name: "Email code" }).click();
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByRole("button", { name: "Email me a code" }).click();
    await typeDigits(page, "123456");
    await expect(page.getByTestId("two-factor-step")).toBeVisible();
    expect(await sessionToken(page)).toBeNull();
    await page.keyboard.type("123456");
    await page.waitForURL(HOME);
    expect(gw.called("POST", "/auth/otp/verify")).toHaveLength(1);
    expect(gw.called("POST", "/auth/2fa/verify")[0]?.body?.challenge).toBe(CHALLENGE);
    expect(await sessionToken(page)).toBe(SESSION);
  });

  test("coming back from Google with a challenge shows the step, removes the challenge from the address bar and opens the session on a right code", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await page.goto(`/signin/complete#challenge=${CHALLENGE}&expires=2026-10-21T12:05:00Z`);
    await expect(page.getByTestId("two-factor-step")).toBeVisible();
    expect(page.url()).not.toContain("qc_");
    expect(page.url()).not.toContain("#");
    expect(await sessionToken(page)).toBeNull();
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    expect(gw.called("POST", "/auth/2fa/verify")[0]?.body).toEqual({ challenge: CHALLENGE, code: "123456" });
    expect(await sessionToken(page)).toBe(SESSION);
  });

  test("on the Google page 'Start again' goes to the sign-in page", async ({ page }) => {
    await productGateway(new MockGateway(), withTwoFactor()).gw.install(page);
    await page.goto(`/signin/complete#challenge=${CHALLENGE}&expires=2026-10-21T12:05:00Z`);
    await page.getByTestId("two-factor-restart").click();
    await page.waitForURL(/\/signin$/);
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("a person without two-factor signs in as before, with no step", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    expect(gw.called("POST", "/auth/2fa/verify")).toHaveLength(0);
    expect(await sessionToken(page)).toBe(SESSION);
  });

  test("the step reads right to left in Arabic, the boxes stay left to right, and it fits a phone", async ({ page }) => {
    await productGateway(new MockGateway(), withTwoFactor()).gw.install(page);
    await page.goto("/signin");
    await pickLanguage(page, "ar");
    await page.getByLabel("البريد الإلكتروني").fill("ada@example.com");
    await page.getByLabel("كلمة المرور", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "تسجيل الدخول", exact: true }).click();
    await expect(page.getByRole("heading", { name: "التحقق بخطوتين" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await expect(page.getByRole("group", { name: "رمز من 6 أرقام" })).toHaveAttribute("dir", "ltr");
    await page.setViewportSize({ width: 360, height: 740 });
    await expect.poll(() => sideways(page)).toBeLessThanOrEqual(0);
  });
});

test.describe("the security page", () => {
  test("turning it on: the QR code and the key, the first code, then ten recovery codes that can only be left after they are saved", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.getByTestId("user-menu").click();
    await page.getByTestId("menu-security").click();
    await page.waitForURL("**/app/security*");
    await expect(page.getByTestId("two-factor-status")).toHaveText("Off");

    await page.getByTestId("two-factor-enable").click();
    const dialog = page.getByTestId("two-factor-setup");
    // The QR code is drawn here (an svg, not an image from anywhere), and the key is there to type.
    await expect(dialog.locator("svg[data-testid='qr-code']")).toBeVisible();
    await expect(dialog.locator("img")).toHaveCount(0);
    await expect(page.getByTestId("two-factor-secret")).toHaveValue(TOTP_SECRET);
    expect(gw.called("POST", "/auth/2fa/setup")).toHaveLength(1);
    // Nothing but the gateway has been asked (the secret is not sent to a QR service).
    expect(gw.calls.every((c) => c.path.startsWith("/") && !c.path.includes("qr"))).toBe(true);
    await dialog.getByTestId("two-factor-next").click();

    // A wrong first code is refused and keeps the dialog on that step.
    await typeDigits(page, "000000");
    await expect(dialog.getByRole("alert")).toContainText("Wrong or expired code.");
    expect(state.twoFactor.enabled).toBe(false);
    await page.keyboard.type("123456");
    const codes = dialog.getByTestId("recovery-code-list").locator("li");
    await expect(codes).toHaveCount(10);
    expect(state.twoFactor.enabled).toBe(true);
    expect(gw.called("POST", "/auth/2fa/confirm").at(-1)?.body).toEqual({ code: "123456" });

    // The dialog cannot be closed from outside while the codes are shown, and "Done" waits for the tick.
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Close" })).toHaveCount(0);
    const done = dialog.getByTestId("codes-done");
    await expect(done).toBeDisabled();

    // The file has the address and the day on its first line and every code once.
    const download = page.waitForEvent("download");
    await dialog.getByTestId("codes-download").click();
    const file = await download;
    expect(file.suggestedFilename()).toBe("payrudder-recovery-codes.txt");
    const text = readFileSync((await file.path()) ?? "", "utf8");
    expect(text.split("\n")[0]).toMatch(/^Payrudder recovery codes for ada@example\.com, 2026-10-21$/);
    for (const code of state.twoFactor.recovery) expect(text).toContain(code);

    await dialog.getByTestId("codes-saved").click();
    await expect(done).toBeEnabled();
    await done.click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByTestId("two-factor-status")).toHaveText("On");
    await expect(page.getByTestId("two-factor-left")).toHaveText("10");
    await expect(page.getByTestId("two-factor-low")).toHaveCount(0);
    // The secret and the codes are not kept anywhere in the page's storage.
    const stored = await page.evaluate(() => JSON.stringify([{ ...localStorage }, { ...sessionStorage }]));
    expect(stored).not.toContain(TOTP_SECRET);
    for (const code of state.twoFactor.recovery) expect(stored).not.toContain(code);
  });

  test("closing the setup before the end throws it away: the next try makes a new secret and the person is still off", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/security");
    await page.getByTestId("two-factor-enable").click();
    await expect(page.getByTestId("two-factor-secret")).toBeVisible();
    await page.getByTestId("two-factor-setup").getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByTestId("two-factor-setup")).toHaveCount(0);
    expect(state.twoFactor.enabled).toBe(false);
    await page.getByTestId("two-factor-enable").click();
    await expect(page.getByTestId("two-factor-secret")).toBeVisible();
    expect(gw.called("POST", "/auth/2fa/setup")).toHaveLength(2);
    expect(gw.called("POST", "/auth/2fa/confirm")).toHaveLength(0);
  });

  test("a deployment without a secrets key says so in the dialog", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    gw.on((c) => (c.path === "/auth/2fa/setup" ? { status: 503, body: { error: { code: "UNAVAILABLE", key: "identity.two_factor.not_configured", message: "no secrets key" } } } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/security");
    await page.getByTestId("two-factor-enable").click();
    await expect(page.getByTestId("two-factor-setup").getByRole("alert")).toContainText("has no secrets key");
  });

  test("shows when it was turned on and how many recovery codes are left, and warns at three or fewer", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor({ recovery: ["a-1", "a-2", "a-3"] }));
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    await page.goto("/app/security");
    await expect(page.getByTestId("two-factor-status")).toHaveText("On");
    await expect(page.getByTestId("two-factor-since")).toContainText("2026");
    await expect(page.getByTestId("two-factor-left")).toHaveText("3");
    await expect(page.getByTestId("two-factor-low")).toContainText("Only 3 recovery codes left");
  });

  test("new recovery codes need a code, replace the old set and are shown once", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    await page.goto("/app/security");
    await page.getByTestId("two-factor-regenerate").click();
    const prompt = page.getByTestId("code-prompt");
    await prompt.getByLabel("Code").fill("999999");
    await prompt.getByTestId("code-prompt-submit").click();
    await expect(prompt.getByRole("alert")).toContainText("Wrong or expired code.");
    await prompt.getByLabel("Code").fill("123456");
    await prompt.getByTestId("code-prompt-submit").click();
    const shown = page.getByTestId("new-recovery-codes");
    await expect(shown.getByTestId("recovery-code-list").locator("li")).toHaveCount(10);
    await expect(shown).toContainText("fresh0-abcd");
    expect(state.twoFactor.recovery[0]).toBe("fresh0-abcd");
    await page.keyboard.press("Escape");
    await expect(shown).toBeVisible();
    await expect(shown.getByTestId("codes-done")).toBeDisabled();
    await shown.getByTestId("codes-saved").click();
    await shown.getByTestId("codes-done").click();
    await expect(shown).toHaveCount(0);
    expect(gw.called("POST", "/auth/2fa/recovery-codes").map((c) => c.body?.code)).toEqual(["999999", "123456"]);
  });

  test("turning it off needs a code, and works with a recovery code", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), withTwoFactor());
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    await page.goto("/app/security");
    await page.getByTestId("two-factor-disable").click();
    const prompt = page.getByTestId("code-prompt");
    await prompt.getByLabel("Code").fill("111111");
    await prompt.getByTestId("code-prompt-submit").click();
    await expect(prompt.getByRole("alert")).toContainText("Wrong or expired code.");
    expect(state.twoFactor.enabled).toBe(true);
    await prompt.getByLabel("Code").fill("old1-abcd");
    await prompt.getByTestId("code-prompt-submit").click();
    await expect(page.getByTestId("code-prompt")).toHaveCount(0);
    await expect(page.getByTestId("two-factor-status")).toHaveText("Off");
    expect(state.twoFactor.enabled).toBe(false);
    // Signing in again asks for no second step.
    await expect(page.getByTestId("two-factor-enable")).toBeVisible();
  });

  test("while an organisation requires it, turning it off is not possible and the page says why", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), withTwoFactor({ required: true }));
    await gw.install(page);
    await passwordSignIn(page);
    await typeDigits(page, "123456");
    await page.waitForURL(HOME);
    await page.goto("/app/security");
    await expect(page.getByTestId("two-factor-disable")).toBeDisabled();
    await expect(page.getByTestId("two-factor-required")).toContainText("requires two-factor sign-in");
    await expect(page.getByTestId("two-factor-regenerate")).toBeEnabled();
  });

  test("is linked from the person's menu, in Arabic too, and fits a phone", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await pickLanguage(page, "ar");
    await page.goto("/app/security");
    await expect(page.getByRole("heading", { name: "الأمان" })).toBeVisible();
    await expect(page.getByTestId("two-factor-card")).toContainText("تسجيل الدخول بخطوتين");
    await page.getByTestId("two-factor-enable").click();
    await expect(page.getByTestId("two-factor-setup").locator("svg[data-testid='qr-code']")).toBeVisible();
    await page.setViewportSize({ width: 360, height: 740 });
    await expect.poll(() => sideways(page)).toBeLessThanOrEqual(0);
    const box = await page.getByTestId("two-factor-setup").boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(360);
  });
});

test.describe("a person who must set it up", () => {
  test("is held on the security page: every other address leads there, with a banner that says why", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { twoFactor: { enabled: false, required: true, recovery: [], started: false, code: "123456", enableTime: null } });
    await gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app/security*");
    await expect(page.getByTestId("setup-required")).toContainText("requires two-factor sign-in");
    for (const path of ["/app", "/app/settings", "/app/members", "/operator"]) {
      await page.goto(path);
      await page.waitForURL("**/app/security*");
    }
    // Setting it up releases them: the banner goes and the rest of the console opens.
    await page.getByTestId("two-factor-enable").click();
    await page.getByTestId("two-factor-next").click();
    await typeDigits(page, "123456");
    await page.getByTestId("codes-saved").click();
    await page.getByTestId("codes-done").click();
    await expect(page.getByTestId("setup-required")).toHaveCount(0);
    await page.goto("/app/settings");
    await expect(page).toHaveURL(/\/app\/settings/);
    await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  });

  test("a request the gateway refuses with setup_required sends a person who was working to the security page", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    // The organisation's policy changes under them: the profile still says nothing, but the gateway now refuses every route but /auth.
    state.twoFactor.required = true;
    await page.getByTestId("sidebar").getByRole("link", { name: "Members" }).click();
    await page.waitForURL("**/app/security*");
    await expect(page.getByTestId("setup-required")).toBeVisible();
  });

  test("the pages a mailed link opens still work, and signing out is still possible", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { twoFactor: { enabled: false, required: true, recovery: [], started: false, code: "123456", enableTime: null } });
    await gw.install(page);
    await page.goto("/signin");
    await page.getByLabel("E-mail").fill("ada@example.com");
    await page.getByLabel("Password", { exact: true }).fill("right password!");
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/app/security*");
    // The link of a mail is its own credential: it is followed (the address is verified) and the person is not bounced away from the page before that.
    await page.goto("/verify#token=qv_good");
    await expect.poll(() => gw.called("POST", "/auth/verify").length).toBe(1);
    await expect(page.getByText("E-mail address confirmed")).toBeVisible();
    expect(page.url()).toContain("/verify");
    await page.goto("/app/security");
    await page.getByTestId("user-menu").click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await page.waitForURL(/\/signin/);
    expect(await sessionToken(page)).toBeNull();
  });
});

test.describe("the organisation's policy", () => {
  test("an owner can require two-factor sign-in of the owners; it is off by default and helper text says what happens", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/settings");
    const toggle = page.getByTestId("require-two-factor");
    await expect(toggle).not.toBeChecked();
    await expect(page.getByText("held on the security page")).toBeVisible();
    await toggle.click();
    await expect(toggle).toBeChecked();
    expect(gw.called("PATCH", "/app/v1/account/security")[0]?.body).toEqual({ require_two_factor_for_owners: true });
    expect(state.requireTwoFactor).toBe(true);
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    expect(gw.called("PATCH", "/app/v1/account/security").at(-1)?.body).toEqual({ require_two_factor_for_owners: false });
  });

  test("an administrator does not see the switch", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "admin" });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto("/app/settings");
    await expect(page.getByTestId("account-name")).toBeVisible();
    await expect(page.getByTestId("require-two-factor")).toHaveCount(0);
  });
});
