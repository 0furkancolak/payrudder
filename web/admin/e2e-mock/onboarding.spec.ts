import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { productGateway, signInAsAda } from "./product";

// The first-run wizard: a new sign-up (no app, never started it) sees why Payrudder exists, makes the organisation's first app from one short form and is
// pointed at what comes next. Progress is kept per person, it can be skipped and resumed, and the "Getting started" card on the home ticks from the
// person's data (meters, packages and keys are made there, not in the wizard).

const wizard = (page: Page) => page.getByTestId("onboarding");

// A new account starts on the currency of the browser's time zone; these tests pin one that has no mapping, so the fallback (USD) applies.
test.use({ timezoneId: "UTC" });

test.describe("the first-run wizard", () => {
  test("a new sign-up learns why Payrudder exists and makes the first app from one form", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), { apps: [], keys: [], accountName: "Ada Labs" });
    await gw.install(page, { onboarded: false, tourSeen: false });
    await signInAsAda(page);

    // Why: the four things Payrudder does.
    await expect(wizard(page)).toBeVisible();
    // The tour of the home waits for the wizard to be over.
    await expect(page.getByTestId("tour")).toHaveCount(0);
    await expect(wizard(page).getByRole("heading", { name: "Bill for what your customers use" })).toBeFocused();
    const flow = wizard(page).getByTestId("onb-flow").getByRole("listitem");
    await expect(flow).toHaveCount(4);
    await expect(flow.nth(0)).toContainText("Meter usage");
    await expect(flow.nth(3)).toContainText("Get paid");
    await page.getByTestId("onb-start").click();

    // One form: the organisation's name, the app's name, and the currency and time zone that belong to the app.
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-app-name")).toHaveValue("My first app");
    await wizard(page).getByTestId("onb-org-name").fill("Lovelace Labs");
    await wizard(page).getByTestId("onb-app-name").fill("Docs bot");
    await wizard(page).getByTestId("onb-continue").click();
    await expect.poll(() => state.accountName).toBe("Lovelace Labs");
    expect(gw.called("POST", "/app/v1/apps")[0]?.body).toMatchObject({ name: "Docs bot", currency: "USD" });

    // Done, with the next steps; the home then carries on with the checklist.
    await expect(wizard(page).getByRole("heading", { name: "You are all set" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-next").getByRole("link")).toHaveCount(4);
    await wizard(page).getByTestId("onb-finish").click();
    await expect(wizard(page)).toHaveCount(0);
    // The wizard said what the home's tour would say, so the tour does not start on top of it.
    await expect(page.getByTestId("getting-started")).toBeVisible();
    await expect(page.getByTestId("tour")).toHaveCount(0);
    const card = page.getByTestId("getting-started");
    await expect(card.getByTestId("todo-app")).toHaveAttribute("data-done", "true");
    await expect(card.getByTestId("todo-meter")).toHaveAttribute("data-done", "false");
    await expect(card.getByTestId("todo-package")).toHaveAttribute("data-done", "false");
    await expect(card.getByTestId("todo-key")).toHaveAttribute("data-done", "false");
    await expect(card.getByTestId("getting-started-resume")).toHaveCount(0);

    // Hiding the card keeps it away, also after a reload.
    await card.getByTestId("getting-started-hide").click();
    await expect(card).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("getting-started")).toHaveCount(0);
  });

  test("it is resumable: closing the page on the form and coming back continues there, and the app is made once", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await page.reload();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await wizard(page).getByTestId("onb-continue").click();
    await expect(wizard(page).getByRole("heading", { name: "You are all set" })).toBeVisible();
    expect(gw.called("POST", "/app/v1/apps")).toHaveLength(1);
  });

  test("it can be skipped, and the checklist on the home then offers to continue where it stopped", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await page.getByTestId("onb-skip").click();

    await expect(wizard(page)).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    const card = page.getByTestId("getting-started");
    await expect(card.getByTestId("getting-started-count")).toHaveText("0 of 5 done");
    // It does not come back by itself ...
    await page.reload();
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await expect(wizard(page)).toHaveCount(0);
    // ... but the card brings it back, at the step it stopped at.
    await page.getByTestId("getting-started-resume").click();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    expect(gw.called("POST", "/app/v1/apps")).toHaveLength(0);
  });

  test("it never opens for somebody who has an app, nor for a viewer, and it is switched off by the support switch", async ({ page, context }) => {
    // Somebody with an app: the home, no wizard, no card.
    const withApp = productGateway(new MockGateway());
    await withApp.gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(wizard(page)).toHaveCount(0);
    await expect(page.getByTestId("getting-started")).toHaveCount(0);

    // A viewer with no app: nothing to create, so nothing to set up.
    const viewerPage = await context.newPage();
    const viewer = productGateway(new MockGateway(), { apps: [], role: "viewer" });
    await viewer.gw.install(viewerPage, { onboarded: false, tourSeen: true });
    await signInAsAda(viewerPage);
    await expect(viewerPage.getByText("An owner or administrator can create one.")).toBeVisible();
    await expect(viewerPage.getByTestId("onboarding")).toHaveCount(0);

    // The support switch.
    const quiet = await context.newPage();
    await quiet.addInitScript(() => localStorage.setItem("payrudder.noWelcome", "1"));
    await productGateway(new MockGateway(), { apps: [] }).gw.install(quiet, { onboarded: false, tourSeen: false });
    await signInAsAda(quiet);
    await expect(quiet.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await expect(quiet.getByTestId("onboarding")).toHaveCount(0);
    await expect(quiet.getByTestId("tour")).toHaveCount(0);
  });

  test("it speaks the person's language", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    // The wizard has its own language menu, since it covers the page's.
    await wizard(page).getByTestId("language-switcher").click();
    await page.getByTestId("lang-tr").click();
    await expect(wizard(page).getByRole("heading", { name: "Müşterilerinizin kullandığı kadar faturalayın" })).toBeVisible();
    await expect(wizard(page).getByTestId("onb-start")).toContainText("Hesabımı kur");
  });
});

// The progress is the person's, kept by the server (GET and PATCH /app/v1/me/onboarding), so another browser continues where the first one stopped. The
// browser's own storage stays the fast copy and what there is when the server cannot be reached.
test.describe("kept on the server", () => {
  test("a second browser continues where the first stopped, and a wizard that was skipped does not open in a third", async ({ page, browser }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await page.getByTestId("onb-start").click();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await expect.poll(() => [...gw.onboarding.values()][0]?.wizard?.step).toBe("app");

    // Another browser: its own storage is empty, the person is the same.
    const second = await browser.newContext();
    const secondPage = await second.newPage();
    await gw.install(secondPage, { onboarded: false, tourSeen: true });
    await signInAsAda(secondPage);
    await expect(secondPage.getByTestId("onboarding").getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await secondPage.getByTestId("onb-skip").click();
    await expect(secondPage.getByTestId("onboarding")).toHaveCount(0);
    await expect.poll(() => [...gw.onboarding.values()][0]?.wizard?.status).toBe("skipped");

    // A third one never shows the wizard that was skipped.
    const third = await browser.newContext();
    const thirdPage = await third.newPage();
    await gw.install(thirdPage, { onboarded: false, tourSeen: true });
    await signInAsAda(thirdPage);
    await expect(thirdPage.getByRole("heading", { name: "Create your first app" })).toBeVisible();
    await expect(thirdPage.getByTestId("onboarding")).toHaveCount(0);
    await expect(thirdPage.getByTestId("getting-started-resume")).toBeVisible();
    await second.close();
    await third.close();
  });

  test("what this browser knew before the server kept it is sent up, and the wizard goes on from this browser when the server is down", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [] });
    // Progress that only this browser has: the wizard on its form.
    await page.addInitScript(() => {
      const key = "payrudder.onboarding.11111111-1111-4111-8111-111111111111";
      if (localStorage.getItem(key) === null) localStorage.setItem(key, JSON.stringify({ status: "active", step: "app" }));
    });
    await gw.install(page, { onboarded: false, tourSeen: true });
    await signInAsAda(page);
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
    await expect.poll(() => [...gw.onboarding.values()][0]?.wizard).toMatchObject({ status: "active", step: "app" });

    // The server goes away: the wizard goes on from the browser's copy.
    gw.onboardingDown = true;
    await page.reload();
    await expect(wizard(page).getByRole("heading", { name: "Set up your first app" })).toBeVisible();
  });
});
