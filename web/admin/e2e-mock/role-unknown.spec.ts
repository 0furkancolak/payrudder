import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, productGateway, signInAsAda } from "./product";

// The role is not known until the profile has loaded (viewer-gating.spec.ts has the pages for a role that is): until then nothing is offered, so that a viewer
// never sees a moment of controls the server would refuse.

test.use({ viewport: { width: 1440, height: 900 } });

/** Holds back the account (and the app, whose role is the account's) until `release` is called. */
async function holdProfile(page: Page): Promise<() => void> {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  for (const url of ["**/gw/app/v1/account", `**/gw/app/v1/apps/${APP}`]) {
    await page.route(url, async (route) => {
      await held;
      await route.fallback();
    });
  }
  return release;
}

test("API keys: until the account has said who the person is nobody is offered a new key or ticks", async ({ page }) => {
  const { gw } = productGateway(new MockGateway());
  await gw.install(page);
  await signInAsAda(page);
  const release = await holdProfile(page);
  await page.goto(`/app/apps/${APP}/keys?env=live`);
  await expect(page.getByTestId("keys-env")).toBeVisible();
  await expect(page.getByTestId("new-live-key")).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toHaveCount(0);
  release();
  await expect(page.getByTestId("new-live-key")).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Select all rows on this page" })).toBeVisible();
});

test("organisation home: until the account has said who the person is nobody is offered a new app", async ({ page }) => {
  const { gw } = productGateway(new MockGateway());
  await gw.install(page);
  await signInAsAda(page);
  const release = await holdProfile(page);
  await page.goto("/app?app=all");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create app" })).toHaveCount(0);
  release();
  await expect(page.getByTestId("header-actions").getByRole("button", { name: "Create app" })).toBeVisible();
});
