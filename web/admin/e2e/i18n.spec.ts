import { expect, test } from "@playwright/test";

const operatorToken = process.env.QUOTIFY_OPERATOR_TOKEN ?? "local-operator-token-change-me-0123456789";

test.describe("languages", () => {
  test("the language follows the browser, can be changed, and is remembered", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Operator console" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");

    await page.getByLabel("Language").selectOption("tr");
    await expect(page.getByRole("heading", { name: "Operatör konsolu" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.locator("html")).toHaveAttribute("dir", "ltr");

    await page.reload();
    await expect(page.getByRole("heading", { name: "Operatör konsolu" })).toBeVisible();
  });

  test("Arabic reads right to left", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Language").selectOption("ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await expect(page.getByRole("heading", { name: "وحدة تحكم المشغّل" })).toBeVisible();
  });

  test("a visitor whose browser speaks German gets German", async ({ browser }) => {
    const context = await browser.newContext({ locale: "de-DE" });
    const page = await context.newPage();
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Operator-Konsole" })).toBeVisible();
    await context.close();
  });

  test("the signed-in pages and the playground are translated too", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Language").selectOption("fr");
    await page.getByLabel("Jeton opérateur").fill(operatorToken);
    await page.getByRole("button", { name: "Se connecter" }).click();
    await expect(page.getByRole("heading", { name: "Locataires" })).toBeVisible();
    await page.getByRole("link", { name: "Bac à sable" }).click();
    await expect(page.getByRole("heading", { name: "Bac à sable" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Créer le compte" })).toBeVisible();
    // A development stack pre-fills the account form; the field is labelled in French.
    await expect(page.getByLabel("Devise")).toHaveValue("TRY");
  });
});
