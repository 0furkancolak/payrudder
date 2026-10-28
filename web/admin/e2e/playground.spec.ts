import { expect, test } from "@playwright/test";
import { pickCurrency } from "../e2e-mock/ui";
import { signInAsOperator } from "./operator";

test("the playground takes a visitor from an empty account to a paid invoice", async ({ page }) => {
  test.setTimeout(300_000);
  // The operator is a person: the dev stack's first user signs in with a password and opens the console.
  await signInAsOperator(page);
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
  await page.getByRole("link", { name: "Playground", exact: true }).click();

  // The account form comes pre-filled too; overwrite it to prove each field still takes input.
  await page.getByTestId("playground-account-open").click();
  const form = page.getByRole("form", { name: "new account" });
  await form.getByLabel("Account name").fill(`playground-${Date.now()}`);
  await form.getByLabel("Time zone").fill("Europe/Istanbul");
  await form.getByLabel("Grace period (hours)").fill("24");
  await pickCurrency(form.getByLabel("Currency"), "TRY");
  await page.getByRole("dialog").getByRole("button", { name: "Create account" }).click();
  await expect(page.getByText("Sandbox API key issued")).toBeVisible();

  await page.getByRole("button", { name: /Create meter, plan/ }).click();
  await expect(page.getByText("Subscription to starter")).toBeVisible();

  await page.getByLabel("Calls").fill("250");
  await page.getByRole("button", { name: "Send usage" }).click();
  await expect(page.getByTestId("sent")).toHaveText("250 calls sent");

  await page.getByRole("button", { name: "Close period now" }).click();
  const invoice = page.getByTestId("playground-invoice").first();
  await expect(invoice).toContainText("COMPLETED", { timeout: 240_000 });
  await expect(invoice).toContainText("paid");

  await invoice.getByRole("link", { name: "Open" }).click();
  await expect(page.getByTestId("saga-event").last()).toContainText(/completed/i);
});

test("a declined payment outcome leaves the invoice open with the payment declined", async ({ page }) => {
  test.setTimeout(300_000);
  await signInAsOperator(page);
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
  await page.getByRole("link", { name: "Playground", exact: true }).click();
  await page.getByTestId("playground-account-open").click();
  const form = page.getByRole("form", { name: "new account" });
  await form.getByLabel("Account name").fill(`playground-declined-${Date.now()}`);
  await form.getByLabel("Time zone").fill("Europe/Istanbul");
  await form.getByLabel("Grace period (hours)").fill("24");
  await pickCurrency(form.getByLabel("Currency"), "EUR");
  await page.getByRole("dialog").getByRole("button", { name: "Create account" }).click();
  await page.getByRole("button", { name: /Create meter, plan/ }).click();
  await expect(page.getByText("Subscription to starter")).toBeVisible();
  await page.getByLabel("Payment outcome").selectOption("declined");
  await expect(page.getByText("Payment outcome: declined")).toBeVisible();
  await page.getByRole("button", { name: "Send usage" }).click();
  await expect(page.getByTestId("sent")).toHaveText("42 calls sent");
  await page.getByRole("button", { name: "Close period now" }).click();
  const invoice = page.getByTestId("playground-invoice").first();
  await expect(invoice).toContainText("PAYMENT_DECLINED", { timeout: 240_000 });
  await expect(invoice).toContainText("open");
});
