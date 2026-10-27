import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, productGateway, signInAsAda } from "./product";

// The Payrudder brand (phase 48, payrudder-brand/DESIGN.md): the name, the logo files that switch with the theme, a rail that is dark in both themes,
// and no horizontal overflow at phone, tablet and desktop widths. SHOTS=<dir> also writes the screenshots (.playwright-mcp/ in the repository).
const shots = process.env.SHOTS;
const widths = [375, 768, 1280] as const;

async function overflows(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
}

test.describe("brand", () => {
  test("the page says Payrudder and links the favicon, the touch icon, the manifest and a theme colour", async ({ page }) => {
    await productGateway(new MockGateway()).gw.install(page);
    await page.goto("/signin");
    await expect(page).toHaveTitle(/Payrudder$/);
    await expect(page.locator('link[rel="icon"][href="/brand/favicon.ico"]')).toHaveCount(1);
    await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute("href", "/brand/payrudder-icon-180.png");
    await expect(page.locator('link[rel="manifest"]')).toHaveAttribute("href", "/manifest.webmanifest");
    await expect(page.locator('meta[name="theme-color"]')).toHaveCount(2);
    await expect(page.getByRole("img", { name: "Payrudder" }).first()).toBeVisible();
    await expect(page.getByText(new RegExp("Quo" + "tify"))).toHaveCount(0);
  });

  for (const scheme of ["light", "dark"] as const) {
    test(`${scheme}: the wordmark of the theme shows on the sign-in page and the rail is dark`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await productGateway(new MockGateway()).gw.install(page);
      await page.goto("/signin");
      const visible = page.locator('img[src$="payrudder-wordmark-' + scheme + '.svg"]');
      await expect(visible.first()).toBeVisible();
      await signInAsAda(page);
      await page.setViewportSize({ width: 1280, height: 800 });
      const rail = await page.getByTestId("sidebar").evaluate((el) => getComputedStyle(el).backgroundColor);
      // Dark in both themes: the red, green and blue channels are all low.
      const nums = rail.match(/[\d.]+/g)!.map(Number);
      // Chromium reports the token as oklch(L C H): lightness is the first number; an rgb() answer has 0-255 channels.
      expect(rail.startsWith("oklch") ? (nums[0] ?? 1) < 0.3 : Math.max(...nums.slice(0, 3)) < 60).toBe(true);
    });
  }

  for (const w of widths) {
    test(`${w}px: sign-in, organisation home and dashboard do not overflow`, async ({ page }) => {
      await page.setViewportSize({ width: w, height: w < 600 ? 800 : 900 });
      await productGateway(new MockGateway()).gw.install(page);
      await page.goto("/signin");
      await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
      expect(await overflows(page)).toBe(false);
      if (shots) await page.screenshot({ path: `${shots}/brand-signin-${w}.png`, fullPage: true });
      await signInAsAda(page);
      await expect(page.getByRole("heading").first()).toBeVisible();
      expect(await overflows(page)).toBe(false);
      if (shots) await page.screenshot({ path: `${shots}/brand-org-home-${w}.png`, fullPage: true });
      await page.goto(`/app/apps/${APP}`);
      await expect(page.getByTestId("kpi-revenue-value")).toBeVisible();
      expect(await overflows(page)).toBe(false);
      if (shots) await page.screenshot({ path: `${shots}/brand-dashboard-${w}.png`, fullPage: true });
    });
  }
});
