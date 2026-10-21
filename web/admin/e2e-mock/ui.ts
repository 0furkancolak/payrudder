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

/** Chooses a language in the header's language menu. */
export async function pickLanguage(page: Page, code: string): Promise<void> {
  await page.getByTestId("language-switcher").click();
  await page.getByTestId(`lang-${code}`).click();
}
