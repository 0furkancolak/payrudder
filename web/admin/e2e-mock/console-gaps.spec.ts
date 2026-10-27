import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";
import { pickCurrency, pickPlace, pickZone } from "./ui";

const ACCOUNT = "0192f0b4-0000-7000-8000-000000000001";
const SANDBOX = "0192f0b4-0000-7000-8000-0000000000a1";
const LIVE = TENANT;

/** Switches the console to a language (the saved choice) and reloads; the sign-in form is in English, so this comes after it. */
async function inLanguage(page: Page, locale: string): Promise<void> {
  await page.evaluate((l) => window.localStorage.setItem("payrudder.locale", l), locale);
  await page.reload();
}

const app = (id: string, name: string, slug: string) => ({
  id,
  account_id: ACCOUNT,
  name,
  slug,
  create_time: "2026-10-20T10:00:00Z",
  tenants: [
    { id: SANDBOX, environment: "ENVIRONMENT_SANDBOX", currency: "TRY", timezone: "Europe/Istanbul" },
    { id: LIVE, environment: "ENVIRONMENT_LIVE", currency: "TRY", timezone: "Europe/Istanbul" },
  ],
});

test.describe("apps", () => {
  test("an app is listed, created with a slug and its API key is shown once and revoked", async ({ page }) => {
    const apps = [app("app-1", "Default", "default")];
    const gw = baseTenant(new MockGateway(), { account_id: ACCOUNT }).on((c) => {
      if (c.path === `/admin/v1/accounts/${ACCOUNT}/apps` && c.method === "GET") return { body: { apps } };
      if (c.path === `/admin/v1/accounts/${ACCOUNT}/apps` && c.method === "POST") {
        apps.push(app("app-2", String(c.body?.name), String(c.body?.slug)));
        return { status: 201, body: apps.at(-1) };
      }
      if (c.path === `/admin/v1/tenants/${SANDBOX}/api-keys` && c.method === "POST") {
        return { status: 201, body: { api_key: { id: "key-1", tenant_id: SANDBOX, prefix: "AbCd1234", environment: "ENVIRONMENT_SANDBOX", status: "STATUS_ACTIVE" }, plaintext: "qk_test_AbCd1234_secretsecret" } };
      }
      if (c.path === "/admin/v1/api-keys/key-1" && c.method === "DELETE") return { body: { id: "key-1", status: "STATUS_REVOKED" } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/apps");
    await expect(page.getByRole("row", { name: /Default/ })).toContainText("default");

    await page.getByTestId("apps-new").click();
    const form = page.getByRole("form", { name: "new app" });
    await form.getByLabel("App name").fill("Web shop");
    await form.getByLabel("Slug").fill("Web Shop");
    await expect(page.getByRole("alert")).toContainText("lower-case letters");
    await expect(page.getByRole("dialog").getByRole("button", { name: "Create app" })).toBeDisabled();
    await form.getByLabel("Slug").fill("web-shop");
    await pickCurrency(form.getByLabel("Currency"), "EUR");
    await pickZone(form.getByLabel("Time zone"), "Europe/Berlin");
    await form.getByLabel("Grace period (hours)").fill("48");
    await page.getByRole("dialog").getByRole("button", { name: "Create app" }).click();
    await expect(page.getByRole("row", { name: /Web shop/ })).toContainText("web-shop");
    expect(gw.called("POST", `/admin/v1/accounts/${ACCOUNT}/apps`)[0]?.body).toEqual({ name: "Web shop", slug: "web-shop", currency: "EUR", timezone: "Europe/Berlin", grace_period: "172800s" });

    await page.getByRole("row", { name: /Default/ }).getByRole("button", { name: "API keys" }).click();
    const keys = page.getByRole("region", { name: "API keys of Default" });
    await keys.getByRole("button", { name: "Create a key for sandbox" }).click();
    await expect(page.getByTestId("api-key-secret")).toHaveText("qk_test_AbCd1234_secretsecret");
    await keys.getByRole("button", { name: "I have copied it" }).click();
    await expect(page.getByTestId("api-key-secret")).toHaveCount(0);
    await expect(keys).not.toContainText("secretsecret");
    await expect(keys).toContainText("qk_test_AbCd1234…");

    page.once("dialog", (d) => void d.accept());
    await keys.getByRole("button", { name: "Revoke", exact: true }).click();
    await expect(keys.getByText("Revoked", { exact: true })).toBeVisible();
    expect(gw.called("DELETE", "/admin/v1/api-keys/key-1")).toHaveLength(1);
  });

  test("a key made elsewhere is revoked by its id, and a refusal is shown", async ({ page }) => {
    const gw = baseTenant(new MockGateway(), { account_id: ACCOUNT }).on((c) => {
      if (c.path === `/admin/v1/accounts/${ACCOUNT}/apps`) return { body: { apps: [app("app-1", "Default", "default")] } };
      if (c.path === "/admin/v1/api-keys/gone" && c.method === "DELETE") return { status: 404, body: { error: { code: "NOT_FOUND", key: "common.not_found", message: "api key not found" } } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/apps");
    await page.getByRole("button", { name: "API keys" }).click();
    await page.getByLabel("Key id").fill("gone");
    page.once("dialog", (d) => void d.accept());
    await page.getByRole("button", { name: "Revoke by id" }).click();
    await expect(page.getByRole("alert")).toContainText("It was not found.");
  });
});

test.describe("seller address", () => {
  test("the form follows the country, matches the backend's rules and previews the invoice", async ({ page }) => {
    const gw = baseTenant(new MockGateway(), { address: { line1: "Atatürk Cd. 1", city: "İstanbul", postal_code: "34000", country: "TR" } }).on((c) => {
      if (c.method === "PATCH" && c.path === `/admin/v1/tenants/${TENANT}`) return { body: { id: TENANT, name: "Acme", address: c.body?.address } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/address");
    await page.getByRole("button", { name: "Edit address" }).click();
    const form = page.getByRole("dialog");
    const preview = page.getByTestId("invoice-preview");
    await expect(form.getByLabel("Address line 1")).toHaveValue("Atatürk Cd. 1");
    await expect(preview).toContainText("Acme");
    await expect(preview.getByTestId("preview-line")).toHaveText(["Atatürk Cd. 1", "34000 İstanbul", "TR"]);
    // Turkey's provinces come from the list, and a province offers its districts; the stored city stays until one is picked.
    await expect(form.getByLabel("City")).toHaveValue("İstanbul");
    await pickPlace(form.getByLabel("Province"), "istan");
    await expect(form.getByLabel("Province")).toHaveValue("Istanbul");
    await expect(form.getByLabel("City")).toHaveValue("İstanbul");
    await pickPlace(form.getByLabel("City"), "kadikoy");
    await expect(form.getByLabel("City")).toHaveValue("Kadıköy");
    await expect(preview.getByTestId("preview-line")).toHaveText(["Atatürk Cd. 1", "34000 Kadıköy", "Istanbul", "TR"]);
    // The licence is in NOTICE and docs/geo-data.md; the form shows no credit line.
    await expect(form.getByText(/GeoNames/)).toHaveCount(0);

    // The United States asks for a state and a ZIP code; another country empties the region and the city of the old one.
    await pickPlace(form.getByLabel("Country", { exact: true }), "united states");
    await expect(form.getByLabel("Country", { exact: true })).toHaveValue("United States");
    await expect(form.getByLabel("State", { exact: true })).toHaveValue("");
    await expect(form.getByLabel("City")).toHaveValue("");
    await expect(form.getByLabel("ZIP code")).toBeVisible();
    await pickPlace(form.getByLabel("State", { exact: true }), "calif");
    await form.getByLabel("ZIP code").fill("94105");
    await pickPlace(form.getByLabel("City"), "san fr");
    await expect(preview.getByTestId("preview-line")).toHaveText(["Atatürk Cd. 1", "94105 San Francisco", "California", "US"]);

    // The backend's rules: one line, at most 120 characters (postal code 20).
    await form.getByLabel("Address line 1").fill("x".repeat(121));
    await expect(form.getByRole("alert")).toContainText("at most 120 characters");
    await expect(form.getByRole("button", { name: "Save address" })).toBeDisabled();
    await form.getByLabel("Address line 1").fill("1 Market St");
    // A country the list does not have is taken by its two-letter code; where there are no regions, region and city are plain text.
    await form.getByLabel("Country", { exact: true }).fill("no");
    await expect(page.getByRole("option", { name: "Use the code NO" })).toBeVisible();
    await form.getByLabel("Country", { exact: true }).press("Enter");
    await expect(form.getByLabel("Country", { exact: true })).toHaveValue("NO");
    await form.getByLabel("City").fill("Oslo");
    await form.getByRole("button", { name: "Save address" }).click();
    await expect(page.getByText("Address saved.")).toBeVisible();
    expect(gw.called("PATCH", `/admin/v1/tenants/${TENANT}`)[0]?.body).toEqual({
      address: { line1: "1 Market St", city: "Oslo", postal_code: "94105", country: "NO" },
    });
  });

  test("a refusal from the API is shown in the console's language by its key", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "PATCH") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "common.invalid_argument", message: "address.city: too long" } } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/address");
    await inLanguage(page, "tr");
    await page.getByRole("button", { name: "Adresi düzenle" }).click();
    await page.getByLabel("Şehir").fill("Ankara");
    await page.getByRole("button", { name: "Adresi kaydet" }).click();
    await expect(page.getByRole("alert")).toHaveText("İstekteki bir şey geçerli değil.");
  });
});

test.describe("translated error keys", () => {
  const refuse = (key: string) => (c: { method: string; path: string }) =>
    c.path === "/v1/customers" && c.method === "POST"
      ? { status: 409, body: { error: { code: "ALREADY_EXISTS", key, message: "customer already exists" } } }
      : c.path === "/v1/customers"
        ? { body: { customers: [] } }
        : undefined;

  async function createCustomer(page: Page, name: string, button: string, id: string, mail: string) {
    await page.getByTestId("customers-new").click();
    const form = page.getByRole("form");
    await form.getByLabel(id, { exact: true }).fill("cus_1");
    await form.getByLabel(name, { exact: true }).fill("Ada");
    await form.getByLabel(mail).fill("ada@example.com");
    await page.getByRole("dialog").getByRole("button", { name: button }).click();
  }

  test("a known key is shown in the language of the console, not the server's English", async ({ page }) => {
    await baseTenant(new MockGateway()).on(refuse("common.already_exists")).install(page);
    await openTenantPage(page, "/customers");
    await inLanguage(page, "de");
    await createCustomer(page, "Name", "Kunden anlegen", "Kunden-ID", "E-Mail");
    await expect(page.getByRole("alert")).toHaveText("Existiert bereits.");
  });

  test("an unknown key falls back to the server's message", async ({ page }) => {
    await baseTenant(new MockGateway()).on(refuse("billing.brand_new.key")).install(page);
    await openTenantPage(page, "/customers");
    await inLanguage(page, "de");
    await createCustomer(page, "Name", "Kunden anlegen", "Kunden-ID", "E-Mail");
    await expect(page.getByRole("alert")).toHaveText("customer already exists");
  });
});
