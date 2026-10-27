import { expect, test } from "@playwright/test";
import { MockGateway } from "./gateway";
import { APP, productGateway, signInAsAda } from "./product";

// Renaming and deleting an app on its settings page. The gateway is mocked; the real role rules and the archive are tested in plans-svc and the gateway.
const SETTINGS = `/app/apps/${APP}/settings`;

test.describe("rename an app", () => {
  test("an owner renames it: the name is trimmed, the save button waits for a real change, and the new name shows", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    const input = page.getByTestId("app-name-input");
    const save = page.getByTestId("app-rename-save");
    await expect(input).toHaveValue("Web shop");
    await expect(save).toBeDisabled();
    await input.fill("   ");
    await expect(save).toBeDisabled();
    await input.fill("Web shop");
    await expect(save).toBeDisabled();

    await input.fill("  Storefront ");
    await save.click();
    await expect.poll(() => gw.called("PATCH", `/app/v1/apps/${APP}`)).toHaveLength(1);
    expect(gw.called("PATCH", `/app/v1/apps/${APP}`)[0]?.body).toEqual({ name: "Storefront" });
    // The page reads the app again: the form holds the saved name and has nothing left to save.
    await expect(input).toHaveValue("Storefront");
    await expect(save).toBeDisabled();
    await expect(page.getByRole("link", { name: /Storefront/ }).first()).toBeVisible();
  });

  test("an admin may rename; a viewer sees the name and no form", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway(), { role: "admin" });
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    await expect(page.getByTestId("app-rename")).toBeVisible();
    await expect(page.getByTestId("app-danger-zone")).toHaveCount(0);

    state.role = "viewer";
    await page.reload();
    await expect(page.getByRole("heading", { name: "General" })).toBeVisible();
    await expect(page.getByRole("definition").filter({ hasText: "Web shop" })).toBeVisible();
    await expect(page.getByTestId("app-rename")).toHaveCount(0);
    await expect(page.getByTestId("app-danger-zone")).toHaveCount(0);
  });

  test("a refusal from the server is shown and nothing changes", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    state.role = "viewer"; // the role changed since the page was opened: the server decides
    await page.getByTestId("app-name-input").fill("Sneaky");
    await page.getByTestId("app-rename-save").click();
    await expect(page.getByTestId("app-rename").getByRole("alert")).toBeVisible();
  });
});

test.describe("delete an app", () => {
  test("only the owner sees the danger zone; deleting needs the app's name typed in full, then the app is gone", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    await expect(page.getByTestId("app-danger-zone")).toContainText("Nothing is erased");

    await page.getByTestId("app-delete-open").click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("heading", { name: "Delete Web shop?" })).toBeVisible();
    const confirm = page.getByTestId("app-delete-confirm");
    const typed = page.getByTestId("app-delete-confirm-input");
    await expect(confirm).toBeDisabled();
    await typed.fill("web shop"); // the case counts
    await expect(confirm).toBeDisabled();
    await typed.fill("Web");
    await expect(confirm).toBeDisabled();
    expect(gw.called("DELETE", `/app/v1/apps/${APP}`)).toHaveLength(0);

    await typed.fill("Web shop");
    await expect(confirm).toBeEnabled();
    await confirm.click();
    await expect.poll(() => gw.called("DELETE", `/app/v1/apps/${APP}`)).toHaveLength(1);
    // Back on the organisation's home, which no longer lists the app: this organisation has none left.
    await page.waitForURL(/\/app(\?|$)/);
    await expect(page.getByRole("heading", { name: "Create your first app" })).toBeVisible();
  });

  test("cancelling leaves the app alone, and the box starts empty the next time", async ({ page }) => {
    const { gw } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    await page.getByTestId("app-delete-open").click();
    await page.getByTestId("app-delete-confirm-input").fill("Web shop");
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.getByTestId("app-delete-open").click();
    await expect(page.getByTestId("app-delete-confirm-input")).toHaveValue("");
    expect(gw.called("DELETE", `/app/v1/apps/${APP}`)).toHaveLength(0);
  });

  test("a refusal from the server keeps the dialog open and says why", async ({ page }) => {
    const { gw, state } = productGateway(new MockGateway());
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(SETTINGS);
    await page.getByTestId("app-delete-open").click();
    await page.getByTestId("app-delete-confirm-input").fill("Web shop");
    state.role = "admin"; // demoted meanwhile: the server refuses
    await page.getByTestId("app-delete-confirm").click();
    await expect(page.getByRole("dialog").getByRole("alert")).toBeVisible();
    await expect(page).toHaveURL(/settings/);
  });
});
