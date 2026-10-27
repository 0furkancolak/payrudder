import type { Locator, Page } from "@playwright/test";

/** Chooses a currency in the searchable currency field: types the code and picks the first option that starts with it. */
export async function pickCurrency(field: Locator, code: string): Promise<void> {
  await field.click();
  await field.fill(code);
  await field.page().getByRole("option", { name: new RegExp(`^${code} ·`) }).first().click();
}

/** Chooses a time zone in the searchable time zone field. */
export async function pickZone(field: Locator, zone: string): Promise<void> {
  await field.click();
  await field.fill(zone);
  await field.page().getByRole("option", { name: new RegExp(`^${zone.replace(/_/g, " ")}`) }).first().click();
}

/** Chooses a place (a country, a region or a district) in a searchable address field: types the text and takes the first match with Enter. */
export async function pickPlace(field: Locator, text: string): Promise<void> {
  await field.click();
  await field.fill(text);
  await field.press("Enter");
}

/** Chooses a theme (system, light or dark) in the person's menu. */
export async function pickTheme(page: Page, choice: "system" | "light" | "dark"): Promise<void> {
  await page.getByTestId("user-menu").click();
  await page.getByTestId("menu-theme").click();
  // The sub-menu opens beside its trigger and closes when the pointer wanders off, so the item is chosen from the keyboard.
  const item = page.getByTestId(`theme-${choice}`);
  await item.focus();
  await item.press("Enter");
}

/** Switches between the sandbox and live the deliberate way: the pill opens the dialog, and the switch happens on its confirm button. */
export async function switchEnv(page: Page): Promise<void> {
  await page.getByTestId("env-switch").click();
  await page.getByTestId("env-switch-confirm").click();
}

/** Chooses a language: in the header's menu while signed out, in the person's menu once signed in. */
export async function pickLanguage(page: Page, code: string): Promise<void> {
  const header = page.getByTestId("language-switcher");
  if ((await header.count()) > 0) {
    await header.click();
  } else {
    await page.getByTestId("user-menu").click();
    await page.getByTestId("menu-language").click();
  }
  const item = page.getByTestId(`lang-${code}`);
  if ((await header.count()) > 0) {
    await item.click();
  } else {
    await item.focus();
    await item.press("Enter");
  }
}

/** Runs a row's action: a row with one action shows it as a button, a row with several folds them into its "more actions" menu. */
export async function rowAction(row: Locator, name: string | RegExp): Promise<void> {
  await row.waitFor();
  const menu = row.getByTestId("row-actions");
  if ((await menu.count()) === 0) {
    await row.getByRole("button", { name }).click();
    return;
  }
  await menu.click();
  await row.page().getByRole("menuitem", { name }).click();
}

/**
 * Chooses options of a filter chip: opens the chip by its name and picks the options in turn (an empty name picks the "all" entry by its text), then closes
 * the list. A chip with several values keeps its list open between choices; one with a single value closes it itself.
 */
export async function pickFilters(page: Page, label: string, ...options: (string | RegExp)[]): Promise<void> {
  await page.getByRole("button", { name: label, exact: true }).click();
  const list = page.getByRole("listbox", { name: label });
  for (const option of options) await list.getByRole("option", { name: option, exact: typeof option === "string" }).click();
  if (await list.isVisible()) await page.keyboard.press("Escape");
  await list.waitFor({ state: "hidden" });
}

/** Chooses an option of a filter chip: opens the chip by its name and picks the option (an empty name picks the "all" entry by its text). */
export async function pickFilter(page: Page, label: string, option: string | RegExp): Promise<void> {
  await pickFilters(page, label, option);
}

/** Chooses a preset of a date range chip (today, 7d, 30d, month, lastMonth, year); `nth` picks among several chips on the page. */
export async function pickRange(page: Page, preset: string, nth = 0): Promise<void> {
  await page.getByTestId("date-range").nth(nth).click();
  await page.getByTestId(`range-${preset}`).click();
}
