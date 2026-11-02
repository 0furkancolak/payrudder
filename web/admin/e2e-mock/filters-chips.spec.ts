import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { pickLanguage } from "./ui";

// The filters of a list on a phone and in Arabic (docs/plans/02, "Filters"): the chips that say what narrows the list stay in sight when the filters are
// behind one button, the popover fits 360 px and its Apply is reachable, and nothing is cut or reversed the wrong way in right-to-left.

const customers = [
  { id: "id-1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", country: "TR", billing_currency: "TRY", create_time: "2026-10-01T10:00:00Z" },
  { id: "id-2", external_id: "cus_2", name: "Grace Hopper", email: "grace@example.com", country: "DE", billing_currency: "EUR", create_time: "2026-10-02T10:00:00Z" },
  { id: "id-3", external_id: "cus_3", name: "Linus T", email: "linus@example.com", country: "FI", billing_currency: "EUR", create_time: "2026-10-03T10:00:00Z" },
];
const gateway = () => baseTenant(new MockGateway()).on((c) => (c.method === "GET" && c.path === "/v1/customers" ? { body: { customers } } : undefined));

/** The page scrolls sideways when something is wider than the screen. */
const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

test.describe("at 360 px", () => {
  test.use({ viewport: { width: 360, height: 740 } });

  test("the filter popover fits the screen inside the drawer, Apply is reachable, and the applied filters are chips under the search", async ({ page }) => {
    await gateway().install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customer-cus_1").waitFor();
    await expect(page.getByTestId("applied-filters")).toHaveCount(0);

    await page.getByTestId("filters-open").click();
    const drawer = page.getByTestId("filters-drawer");
    await expect(drawer).toBeVisible();
    await drawer.getByRole("button", { name: "Billing currency", exact: true }).click();
    const list = page.getByRole("listbox", { name: "Billing currency" });
    await expect(list).toBeVisible();
    await list.getByRole("option", { name: "EUR" }).click();
    // The popover is inside the screen, whole, and so is the button that applies the draft.
    const pop = await list.locator("xpath=ancestor::*[@role='dialog'][1]").boundingBox();
    expect(pop).not.toBeNull();
    expect(pop!.x).toBeGreaterThanOrEqual(0);
    expect(pop!.x + pop!.width).toBeLessThanOrEqual(360);
    const apply = page.getByTestId("filter-apply");
    const box = await apply.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(360);
    expect(box!.y + box!.height).toBeLessThanOrEqual(740);
    await apply.click();
    await expect(list).toBeHidden();

    // Shut the drawer: the filter that is on stays in sight as a chip under the search, and the rows follow it.
    await drawer.getByRole("button", { name: "Show results" }).click();
    await expect(drawer).toBeHidden();
    await expect(page.getByTestId("applied-filter")).toHaveText("Billing currency: EUR");
    await expect(page.getByTestId("customer-cus_1")).toHaveCount(0);
    await expect(page.getByTestId("customer-cus_2")).toBeVisible();
    expect(await sideways(page)).toBeLessThanOrEqual(0);

    // The chip's cross takes the filter off without opening anything.
    await page.getByTestId("applied-filter").click();
    await expect(page.getByTestId("applied-filters")).toHaveCount(0);
    await expect(page.getByTestId("customer-cus_1")).toBeVisible();
  });

  test("several applied filters are several chips, each with its own name for a screen reader", async ({ page }) => {
    await gateway().install(page);
    await openTenantPage(page, "/customers?currency=EUR&country=DE,FI");
    await page.getByTestId("customer-cus_2").waitFor();
    const chips = page.getByTestId("applied-filter");
    await expect(chips).toHaveCount(2);
    await expect(page.getByRole("list", { name: "Applied filters" })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Country: .*Germany \+1 ×$/ })).toBeVisible();
    await expect(page.getByRole("button", { name: "Billing currency: EUR ×" })).toBeVisible();
  });
});

test.describe("in Arabic", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("the filter chip, its popover, the search icon and the selection bar mirror, and stay on the screen", async ({ page }) => {
    await gateway().install(page);
    await openTenantPage(page, "/customers?currency=EUR");
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await page.getByTestId("customer-cus_2").waitFor();

    // The search icon sits at the start (right) of its box, and the box's text starts there too.
    const search = await page.getByRole("searchbox").boundingBox();
    const icon = await page.locator("[role=search] svg").first().boundingBox();
    expect(icon!.x + icon!.width / 2).toBeGreaterThan(search!.x + search!.width / 2);

    // A date reads in the order of the language: the day at the right end, then the month, then the year at the left.
    const order = await page.getByTestId("customer-cus_2").locator("td").nth(6).evaluate((td) => {
      const walker = document.createTreeWalker(td, NodeFilter.SHOW_TEXT);
      let node = walker.nextNode() as Text;
      while (node && !node.data.includes("2026")) node = walker.nextNode() as Text;
      const at = (needle: string) => {
        const i = node.data.indexOf(needle);
        const r = document.createRange();
        r.setStart(node, i);
        r.setEnd(node, i + needle.length);
        return r.getBoundingClientRect().x;
      };
      return { day: at("02"), year: at("2026") };
    });
    expect(order.day).toBeGreaterThan(order.year);

    // The cross that clears the filter is after the chip in reading order: left of it.
    const chip = page.locator("button[data-active]").first();
    const cross = page.locator("button[aria-label$=': ×']").first();
    const chipBox = await chip.boundingBox();
    const crossBox = await cross.boundingBox();
    expect(crossBox!.x + crossBox!.width / 2).toBeLessThan(chipBox!.x + chipBox!.width / 2);

    // The popover opens from the chip's start (right) edge and is whole on the screen.
    await chip.click();
    const list = page.getByRole("listbox").first();
    await expect(list).toBeVisible();
    // The layer is drawn outside the page, so it must be told the direction: its options read from the right, and the tick of the chosen one is at the far (left) end.
    await expect(list.locator("xpath=ancestor::*[@dir][1]")).toHaveAttribute("dir", "rtl");
    const option = await list.getByRole("option", { selected: true }).boundingBox();
    const optionText = await list.getByRole("option", { selected: true }).locator("span").first().boundingBox();
    expect(optionText!.x + optionText!.width / 2).toBeGreaterThan(option!.x + option!.width / 2);
    const pop = await list.locator("xpath=ancestor::*[@role='dialog'][1]").boundingBox();
    expect(pop!.x).toBeGreaterThanOrEqual(0);
    expect(pop!.x + pop!.width).toBeLessThanOrEqual(1280);
    // (it slides in: wait for it to settle)
    await expect
      .poll(async () => {
        const settled = await list.locator("xpath=ancestor::*[@role='dialog'][1]").boundingBox();
        return Math.abs(settled!.x + settled!.width - (chipBox!.x + chipBox!.width));
      })
      // (a few pixels of difference between the browsers' text widths and the popper's offset are not the bug: opening from the wrong edge is hundreds off)
      .toBeLessThanOrEqual(8);
    await page.keyboard.press("Escape");

    // The bar of the selection: the clear button is at the far end, which is the left.
    await page.getByRole("checkbox").first().click();
    const bar = await page.getByTestId("bulk-bar").boundingBox();
    const clear = await page.getByTestId("bulk-clear").boundingBox();
    expect(clear!.x + clear!.width / 2).toBeLessThan(bar!.x + bar!.width / 2);
    await expect(page.getByTestId("bulk-live")).not.toHaveText("");
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });

  test("on a phone in Arabic the chips and the popover fit 360 px", async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 740 });
    await gateway().install(page);
    await openTenantPage(page, "/customers?country=DE");
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await page.getByTestId("applied-filter").waitFor();
    const chip = await page.getByTestId("applied-filter").boundingBox();
    expect(chip!.x).toBeGreaterThanOrEqual(0);
    expect(chip!.x + chip!.width).toBeLessThanOrEqual(360);
    await page.getByTestId("filters-open").click();
    await page.getByTestId("filters-drawer").locator("button[data-active]").first().click();
    const pop = page.getByRole("listbox").first().locator("xpath=ancestor::*[@role='dialog'][1]");
    await expect(pop).toBeVisible();
    await expect.poll(async () => (await pop.boundingBox())!.x).toBeGreaterThanOrEqual(0);
    expect((await pop.boundingBox())!.x + (await pop.boundingBox())!.width).toBeLessThanOrEqual(360);
    const apply = (await page.getByTestId("filter-apply").boundingBox())!;
    expect(apply.x).toBeGreaterThanOrEqual(0);
    expect(apply.x + apply.width).toBeLessThanOrEqual(360);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });
});
