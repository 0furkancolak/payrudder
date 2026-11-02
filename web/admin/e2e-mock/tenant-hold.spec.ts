import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, appWithLive, liveGateway, productGateway, signInAsAda } from "./product";

// The owner of an app is told, on every page of the app, when the operator has taken its live environment out of service (plan 14): suspended, frozen or banned. An
// active app says nothing, and the sandbox page of a held app still says it.

test.use({ viewport: { width: 1280, height: 800 } });

for (const [status, hold, text] of [
  ["SUSPENDED", "suspended", "Web shop is suspended: its API keys have stopped"],
  ["FROZEN", "frozen", "Web shop is frozen: you can still read everything"],
  ["BANNED", "banned", "Web shop is banned: its API keys have stopped"],
] as const) {
  test(`a ${hold} live environment is announced to its owner, with the way to write to support`, async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive(status)] });
    liveGateway(gw, { status });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}?env=live`);
    const banner = page.getByTestId(`hold-${hold}`);
    await expect(banner).toContainText(text);
    await expect(banner.getByTestId("hold-support")).toHaveAttribute("href", /^\/app\/support(\?|$)/);
    // The go-live card does not offer to finish steps for an environment that is on hold: it says so.
    await expect(page.getByTestId("go-live-state")).toHaveText("On hold");
    // The sandbox of the same app: the person may be looking at it, and is told all the same.
    await page.goto(`/app/apps/${APP}?env=sandbox`);
    await expect(page.getByTestId(`hold-${hold}`)).toBeVisible();
    // The operator's reason is never in it.
    expect(await page.getByTestId("hold-banner").innerText()).not.toMatch(/fraud|nonpayment|reason/i);
  });
}

test("an active app has no banner", async ({ page }) => {
  const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("ACTIVE")] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/apps/${APP}?env=live`);
  await expect(page.getByRole("button", { name: "Switch app" })).toContainText("Web shop");
  await page.waitForLoadState("networkidle");
  await expect(page.getByTestId("hold-banner")).toHaveCount(0);
});
