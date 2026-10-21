import { expect, type Page } from "@playwright/test";

// The real-stack specs sign in to the operator console the way a person does: e-mail and password on the sign-in page, as an address the gateway lists in
// QUOTIFY_OPERATOR_EMAILS (the dev stack lists the dev seed's first user, user1@example.com, which `make dev` creates with this password). There is no
// token form; QUOTIFY_OPERATOR_TOKEN stays what it always was, the machine credential the specs use to call the API directly.
export const operatorEmail = process.env.QUOTIFY_OPERATOR_EMAIL ?? "user1@example.com";
export const operatorPassword = process.env.QUOTIFY_OPERATOR_PASSWORD ?? "password123";

/** Signs in as the operator user and opens the operator console's tenant list. */
export async function signInAsOperator(page: Page, labels = { email: "E-mail", password: "Password", submit: "Sign in" }): Promise<void> {
  await page.goto("/signin");
  await page.getByLabel(labels.email).fill(operatorEmail);
  await page.getByLabel(labels.password, { exact: true }).fill(operatorPassword);
  await page.getByRole("button", { name: labels.submit }).click();
  await page.waitForURL("**/app");
  await page.goto("/operator");
  await page.waitForURL("**/tenants");
  await expect(page.locator("main")).toBeVisible();
}
