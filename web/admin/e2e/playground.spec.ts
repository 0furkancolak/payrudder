import { expect, test } from "@playwright/test";

const operatorToken = process.env.QUOTIFY_OPERATOR_TOKEN ?? "local-operator-token-change-me-0123456789";

test("the playground takes a visitor from an empty account to a paid invoice", async ({ page }) => {
  test.setTimeout(300_000);
  await page.goto("/operator");
  // A development stack pre-fills the operator token, so signing in needs no typing.
  await expect(page.getByLabel("Operator token")).toHaveValue(operatorToken);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
  await page.getByRole("link", { name: "Playground", exact: true }).click();

  // The account form comes pre-filled too; overwrite it to prove each field still takes input.
  const form = page.getByRole("form", { name: "new account" });
  await form.getByLabel("Account name").fill(`playground-${Date.now()}`);
  await form.getByLabel("Time zone").fill("Europe/Istanbul");
  await form.getByLabel("Grace period (hours)").fill("24");
  await form.getByLabel("Currency").selectOption("TRY");
  await form.getByRole("button", { name: "Create account" }).click();
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
  await expect(page.getByTestId("saga-event").last()).toContainText("COMPLETED");
});

test("a declined payment outcome leaves the invoice open with the payment declined", async ({ page }) => {
  test.setTimeout(300_000);
  await page.goto("/operator");
  await page.getByLabel("Operator token").fill(operatorToken);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();
  await page.getByRole("link", { name: "Playground", exact: true }).click();
  const form = page.getByRole("form", { name: "new account" });
  await form.getByLabel("Account name").fill(`playground-declined-${Date.now()}`);
  await form.getByLabel("Time zone").fill("Europe/Istanbul");
  await form.getByLabel("Grace period (hours)").fill("24");
  await form.getByLabel("Currency").selectOption("EUR");
  await form.getByRole("button", { name: "Create account" }).click();
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
