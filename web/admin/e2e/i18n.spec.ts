import { expect, test } from "@playwright/test";
import { pickLanguage } from "../e2e-mock/ui";
import { signInAsOperator } from "./operator";

test.describe("languages", () => {
  test("the language follows the browser, can be changed, and is remembered", async ({ page }) => {
    await page.goto("/signin");
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");

    await pickLanguage(page, "tr");
    await expect(page.getByRole("heading", { name: "Tekrar hoş geldiniz" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "tr");
    await expect(page.locator("html")).toHaveAttribute("dir", "ltr");

    await page.reload();
    await expect(page.getByRole("heading", { name: "Tekrar hoş geldiniz" })).toBeVisible();
  });

  test("Arabic reads right to left", async ({ page }) => {
    await page.goto("/signin");
    await pickLanguage(page, "ar");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
    await expect(page.getByRole("heading", { name: "أهلاً بعودتك" })).toBeVisible();
  });

  test("a visitor whose browser speaks German gets German", async ({ browser }) => {
    const context = await browser.newContext({ locale: "de-DE" });
    const page = await context.newPage();
    await page.goto("/signin");
    await expect(page.getByRole("heading", { name: "Willkommen zurück" })).toBeVisible();
    await context.close();
  });

  test("the signed-in pages and the playground are translated too", async ({ page }) => {
    await page.goto("/signin");
    await pickLanguage(page, "fr");
    await signInAsOperator(page, { email: "E-mail", password: "Mot de passe", submit: "Se connecter" });
    await expect(page.getByRole("heading", { name: "Locataires" })).toBeVisible();
    await page.getByRole("link", { name: "Bac à sable" }).click();
    await expect(page.getByRole("heading", { name: "Bac à sable" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Créer le compte" })).toBeVisible();
    // A development stack pre-fills the account form; the field is labelled in French.
    await expect(page.getByLabel("Devise")).toHaveValue("TRY");
  });
});
