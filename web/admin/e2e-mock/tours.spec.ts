import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { LIVE, APP, billingGateway, productGateway, signInAsAda } from "./product";
import { pickLanguage } from "./ui";

// The tour engine (phase 52): a spotlight over the real page, a card with the step's words, keyboard control, skippable, remembered per person and
// page. The home's tour starts by itself the first time; every page has the tour icon in its header.

const tour = (page: Page) => page.getByTestId("tour-card");
const title = (page: Page) => page.getByTestId("tour-card").getByRole("heading");
const progress = (page: Page) => page.getByTestId("tour-progress");

function gateway() {
  const { gw } = productGateway(new MockGateway());
  billingGateway(gw);
  gw.on((c) => {
    if (c.path === "/v1/subscriptions" && c.method === "GET") return { body: { subscriptions: [] } };
    if (c.path === "/v1/plans" && c.method === "GET") return { body: { plans: [] } };
    return undefined;
  });
  return gw;
}

test.describe("the tour of the home", () => {
  test("starts by itself the first time, lights up the app switcher, the sidebar, the sandbox switch and the user menu, and is remembered", async ({ page }) => {
    const gw = gateway();
    await gw.install(page, { tourSeen: false });
    await signInAsAda(page);

    await expect(tour(page)).toBeVisible();
    await expect(title(page)).toHaveText("Welcome to Payrudder");
    await expect(progress(page)).toHaveText("Step 1 of 6");
    // The first step is a card in the middle of the page: nothing is lit.
    await expect(page.getByTestId("tour-spotlight")).toHaveCount(0);
    await expect(page.getByTestId("tour-next")).toBeFocused();

    const steps: [string, string][] = [
      ["Your apps", "app-switcher"],
      ["Find your way", "sidebar-nav"],
      ["Sandbox and live", "env-switch"],
      ["You and your organisation", "user-menu"],
      ["A tour on every page", "tour-button"],
    ];
    for (const [name, target] of steps) {
      await page.getByTestId("tour-next").click();
      await expect(title(page)).toHaveText(name);
      // What is lit is the real element: the spotlight sits on its box.
      await expect(page.getByTestId("tour-spotlight")).toBeVisible();
      await expect.poll(async () => {
        const lit = await page.getByTestId("tour-spotlight").boundingBox();
        const real = await page.getByTestId(target).first().boundingBox();
        return !!lit && !!real && lit.x <= real.x && lit.y <= real.y && lit.x + lit.width >= real.x + real.width && lit.y + lit.height >= real.y + real.height;
      }).toBe(true);
    }
    await expect(progress(page)).toHaveText("Step 6 of 6");
    await expect(page.getByTestId("tour-next")).toHaveText("Done");
    await page.getByTestId("tour-next").click();
    await expect(page.getByTestId("tour")).toHaveCount(0);

    // Seen: it does not start again, for this person, on this browser.
    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour")).toHaveCount(0);
    await expect(page.getByTestId("tour-unseen")).toHaveCount(0);
  });

  test("the keyboard drives it: arrows move, Escape skips, Tab stays on the card", async ({ page }) => {
    await gateway().install(page, { tourSeen: false });
    await signInAsAda(page);
    await expect(tour(page)).toBeVisible();

    await page.keyboard.press("ArrowRight");
    await expect(title(page)).toHaveText("Your apps");
    await page.keyboard.press("ArrowLeft");
    await expect(title(page)).toHaveText("Welcome to Payrudder");
    await page.keyboard.press("ArrowLeft");
    await expect(title(page)).toHaveText("Welcome to Payrudder");

    // Tab goes round the card's own buttons and never reaches the page under it.
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press("Tab");
      expect(await page.evaluate(() => !!document.activeElement?.closest('[data-testid="tour-card"]'))).toBe(true);
    }
    await page.keyboard.press("Shift+Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest('[data-testid="tour-card"]'))).toBe(true);

    await page.keyboard.press("Escape");
    await expect(page.getByTestId("tour")).toHaveCount(0);
    // Skipping counts as seen.
    await page.reload();
    await expect(page.getByTestId("app-switcher")).toBeVisible();
    await expect(page.getByTestId("tour")).toHaveCount(0);
  });

  test("the card is described to a screen reader: a labelled modal dialog that says where it is", async ({ page }) => {
    await gateway().install(page, { tourSeen: false });
    await signInAsAda(page);
    const card = tour(page);
    await expect(card).toHaveAttribute("role", "dialog");
    await expect(card).toHaveAttribute("aria-modal", "true");
    await expect(page.getByRole("dialog", { name: "Welcome to Payrudder" })).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Step 1 of 6" })).toBeVisible();
  });

  test("on a phone, the steps about what the screen does not show are passed over", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 });
    await gateway().install(page, { tourSeen: false });
    await signInAsAda(page);
    await expect(title(page)).toHaveText("Welcome to Payrudder");
    await page.getByTestId("tour-next").click();
    // The sidebar is a drawer here: its steps are skipped and the tour goes on to the first thing that is on the screen.
    await expect(title(page)).not.toHaveText("Your apps");
    await expect(title(page)).not.toHaveText("Find your way");
    await expect(title(page)).toHaveText(/Sandbox and live|You and your organisation/);
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("tour")).toHaveCount(0);
  });

  test("it reads right to left in Arabic, where the arrows swap", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    await pickLanguage(page, "ar");
    await page.getByTestId("tour-button").click();
    await expect(tour(page)).toBeVisible();
    await expect(title(page)).toHaveText("مرحبًا بك في Payrudder");
    await expect(progress(page)).toHaveText("الخطوة 1 من 6");
    await page.keyboard.press("ArrowLeft");
    await expect(progress(page)).toHaveText("الخطوة 2 من 6");
    await page.keyboard.press("ArrowRight");
    await expect(progress(page)).toHaveText("الخطوة 1 من 6");
  });
});

test.describe("the tour icon of a page header", () => {
  test("the home's tour can be taken again from its icon", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    await expect(page.getByTestId("tour")).toHaveCount(0);
    await expect(page.getByTestId("tour-button")).toHaveAccessibleName("Take a tour of this page");
    await page.getByTestId("tour-button").click();
    await expect(title(page)).toHaveText("Welcome to Payrudder");
    await page.getByTestId("tour-skip").click();
    await expect(page.getByTestId("tour")).toHaveCount(0);
  });

  test("every page has its own tour, in the page's own words, and a dot says it has not been taken", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    const pages: [string, string, RegExp][] = [
      [`/app/t/${LIVE}/customers`, "Customers", /The people and companies you bill/],
      [`/app/t/${LIVE}/subscriptions`, "Subscriptions", /Which customer is on which package/],
      [`/app/t/${LIVE}/packages`, "Packages", /What customers subscribe to/],
      [`/app/t/${LIVE}/invoices`, "Invoices", /Every invoice, with its state/],
      [`/app/t/${LIVE}/payments`, "Payments", /Every attempt to collect money/],
      [`/app/apps/${APP}/keys`, "API keys", /API keys let your server talk to Payrudder/],
      ["/app/members", "Members", /Who belongs to this organisation/],
      ["/app/settings", "Settings", /Your organisation's name/],
    ];
    for (const [path, name, words] of pages) {
      await page.goto(path);
      const button = page.getByTestId("tour-button");
      await expect(button, path).toBeVisible();
      await expect(page.getByTestId("tour-unseen"), path).toBeVisible();
      await button.click();
      await expect(title(page), path).toHaveText(name);
      await expect(tour(page), path).toContainText(words);
      // The first step of a page is about its title; a page with more steps goes on to what is on it.
      const total = Number((await progress(page).innerText()).replace(/\D+(\d+)$/, "$1"));
      if (total > 1) {
        await page.getByTestId("tour-next").click();
        await expect(progress(page), path).toContainText("Step 2");
      }
      await page.keyboard.press("Escape");
      await expect(page.getByTestId("tour"), path).toHaveCount(0);
      // Seen: the dot is gone, the icon still works.
      await expect(page.getByTestId("tour-unseen"), path).toHaveCount(0);
      await expect(button, path).toBeVisible();
    }
  });

  test("a step lights the control it is about, and a step whose control is missing is a card in the middle", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/packages`);
    await page.getByTestId("tour-button").click();
    await page.getByTestId("tour-next").click();
    await expect(title(page)).toHaveText("Search and filter");
    await expect(page.getByTestId("tour-spotlight")).toBeVisible();
    await page.getByTestId("tour-next").click();
    await expect(title(page)).toHaveText("Add new");
    await expect.poll(async () => {
      const lit = await page.getByTestId("tour-spotlight").boundingBox();
      const real = await page.getByTestId("packages-new").boundingBox();
      return !!lit && !!real && lit.x <= real.x && lit.x + lit.width >= real.x + real.width;
    }).toBe(true);
    await page.getByTestId("tour-next").click();
    await expect(title(page)).toHaveText("Trials and free tiers");
    await expect(tour(page)).toContainText("free tier is a package with no fee");
    await expect(page.getByTestId("tour-spotlight")).toHaveCount(0);
    await expect(page.getByTestId("tour-next")).toHaveText("Done");
  });

  test("the tour is in the language of the page", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/customers`);
    await pickLanguage(page, "de");
    await page.getByTestId("tour-button").click();
    await expect(title(page)).toHaveText("Kunden");
    await expect(tour(page)).toContainText("Die Personen und Firmen, die Sie abrechnen");
    await expect(progress(page)).toHaveText(/Schritt 1 von 3/);
  });

  test("leaving the page ends the tour without counting it as seen", async ({ page }) => {
    await gateway().install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/invoices`);
    await page.getByTestId("tour-button").click();
    await expect(tour(page)).toBeVisible();
    await page.goBack();
    await expect(page.getByTestId("tour")).toHaveCount(0);
    await page.goForward();
    await expect(page.getByTestId("tour-unseen")).toBeVisible();
  });
});
