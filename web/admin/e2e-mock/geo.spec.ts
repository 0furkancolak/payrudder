import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, OPERATOR, openTenantPage } from "./gateway";
import { pickPlace } from "./ui";

// The address forms take the country, the region and the district from the GeoNames lists (GET /app/v1/geo/..., mocked in geo.ts), and still store
// the country code and the region and city as text.

const customer = (id: string, extra: Record<string, unknown> = {}) => ({ id: `id-${id}`, external_id: id, name: `Name ${id}`, email: `${id}@example.com`, create_time: "2026-10-05T10:00:00Z", ...extra });

test.describe("location fields", () => {
  test("a customer's address is picked country, region, district, and sent as text", async ({ page }) => {
    const customers: Record<string, unknown>[] = [];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "POST" && c.path === "/v1/customers") {
        customers.push(customer(String(c.body?.external_id), { country: c.body?.country, address: c.body?.address }));
        return { status: 201, body: customers.at(-1) };
      }
      if (c.path === "/v1/customers") return { body: { customers } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customers-new").click();
    const form = page.getByRole("form", { name: "new customer" });
    await form.getByLabel("Customer id").fill("cus_9");
    await form.getByLabel("Name").fill("Ayşe");
    await form.getByLabel("E-mail").fill("ayse@example.com");

    // The country is found by its name; its regions are asked for once it is chosen, and a region's districts once that is.
    await pickPlace(form.getByLabel("Country"), "turk");
    await expect(form.getByLabel("Country")).toHaveValue(/^(Türkiye|Turkey)$/);
    await expect.poll(() => gw.called("GET", "/app/v1/geo/countries/TR/regions").length).toBe(1);
    const region = form.getByLabel("State / region");
    await expect(region).toHaveAttribute("role", "combobox"); // a plain text box until the regions have come
    await region.click();
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["Ankara", "Istanbul", "İzmir"]);
    await region.fill("izm");
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["İzmir", "Use “izm”"]);
    await region.fill("ist");
    await region.press("Enter");
    await expect(region).toHaveValue("Istanbul");
    await expect.poll(() => gw.called("GET", "/app/v1/geo/regions/TR.34/districts").length).toBe(1);
    // Typing without accents finds the district; a place the list lacks is taken as typed.
    const city = form.getByLabel("City");
    // The city is a plain text box until the districts have come; only then is it the searchable list that the typing below goes to.
    await expect(city).toHaveAttribute("role", "combobox");
    await city.fill("uskudar");
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["Üsküdar"]);
    await city.fill("Moda");
    await expect(page.getByRole("listbox").getByRole("option")).toHaveText(["Use “Moda”"]);
    await city.press("Enter");
    await expect(city).toHaveValue("Moda");
    await pickPlace(city, "besiktas");
    await expect(city).toHaveValue("Beşiktaş");
    await form.getByLabel("Address line 1").fill("Barbaros Blv. 1");

    await page.getByRole("dialog").getByRole("button", { name: "Create customer" }).click();
    await expect.poll(() => gw.called("POST", "/v1/customers").length).toBe(1);
    expect(gw.called("POST", "/v1/customers")[0]?.body).toMatchObject({
      external_id: "cus_9",
      country: "TR",
      address: { line1: "Barbaros Blv. 1", city: "Beşiktaş", region: "Istanbul", country: "TR" },
    });
    // The lists are read with the person's session and nothing else.
    const geo = gw.called("GET", /^\/app\/v1\/geo\//);
    expect(geo.length).toBeGreaterThan(0);
    for (const c of geo) {
      expect(c.headers["authorization"]).toBe(`Bearer ${OPERATOR}`);
      expect(c.headers["x-payrudder-tenant"]).toBeUndefined();
    }
  });

  test("an address saved before the lists loads and saves unchanged", async ({ page }) => {
    // Free text from before: a region in lower case, a city the list does not have, and a country code the list does not have either.
    const old = { line1: "Moda Cd. 5", city: "Moda", region: "istanbul", postal_code: "34710", country: "TR" };
    const customers: Record<string, unknown>[] = [
      customer("cus_1", { country: "TR", address: old }),
      customer("cus_2", { country: "QZ", address: { line1: "1 Unknown Rd", city: "Somewhere", region: "North", country: "QZ" } }),
    ];
    const gw = baseTenant(new MockGateway()).on((c) => {
      const m = /^\/v1\/customers\/(cus_\d)\/address$/.exec(c.path);
      if (c.method === "PUT" && m) return { body: customer(m[1] ?? "", { address: c.body }) };
      if (c.path === "/v1/customers") return { body: { customers } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");

    await page.getByRole("row", { name: /cus_1/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-address").click();
    const edit = page.getByRole("dialog", { name: "Address of cus_1" });
    await expect(edit.getByLabel("Country")).toHaveValue(/^(Türkiye|Turkey)$/);
    await expect(edit.getByLabel("State / region")).toHaveValue("istanbul");
    await expect(edit.getByLabel("City")).toHaveValue("Moda");
    // The stored region is still recognised: its districts are offered.
    await expect.poll(() => gw.called("GET", "/app/v1/geo/regions/TR.34/districts").length).toBe(1);
    await edit.getByRole("button", { name: "Save address" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_1/address").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_1/address")[0]?.body).toEqual(old);

    // A country the list does not know: the region list answers 404 and the fields stay plain text.
    await page.getByRole("row", { name: /cus_2/ }).getByTestId("row-actions").click();
    await page.getByTestId("row-action-address").click();
    const edit2 = page.getByRole("dialog", { name: "Address of cus_2" });
    await expect(edit2.getByLabel("Country")).toHaveValue("QZ");
    await expect(edit2.getByLabel("State / region")).toHaveValue("North");
    await expect(edit2.getByLabel("State / region")).not.toHaveAttribute("role", "combobox");
    await edit2.getByLabel("City").fill("Elsewhere");
    await edit2.getByRole("button", { name: "Save address" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/customers/cus_2/address").length).toBe(1);
    expect(gw.called("PUT", "/v1/customers/cus_2/address")[0]?.body).toEqual({ line1: "1 Unknown Rd", city: "Elsewhere", region: "North", country: "QZ" });
  });

  test("country names follow the console's language", async ({ page }) => {
    const gw = baseTenant(new MockGateway(), { address: { country: "DE", city: "Berlin" } });
    await gw.install(page);
    await openTenantPage(page, "/address");
    await page.evaluate(() => window.localStorage.setItem("payrudder.locale", "tr"));
    await page.reload();
    await page.getByRole("button", { name: "Adresi düzenle" }).click();
    const form = page.getByRole("dialog");
    await expect(form.getByLabel("Ülke", { exact: true })).toHaveValue("Almanya");
    await form.getByLabel("Ülke", { exact: true }).fill("isveç");
    await expect(page.getByRole("option", { name: "İsveç" })).toBeVisible();
    await form.getByLabel("Ülke", { exact: true }).press("Escape");
    // The licence stays in NOTICE and docs/geo-data.md; the form carries no credit line.
    await expect(form.getByText(/GeoNames/)).toHaveCount(0);
  });
});
