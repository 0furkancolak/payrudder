import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";

const pack = (id: string, name: string, credits: string, price: string, validity = 0) => ({
  id,
  name,
  credits,
  price_minor: price,
  currency: "USD",
  active: true,
  validity_days: validity,
  create_time: "2026-10-21T09:00:00Z",
});

const ada = { id: "id-cus_1", external_id: "cus_1", name: "Ada", email: "ada@example.com" };

test.describe("credit purchases", () => {
  test("a pack with a validity gets a purchase link whose address is shown once, and the price is never sent", async ({ page }) => {
    const packs = [pack("p1", "Starter", "1000", "500", 30)];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "POST" && c.path === "/v1/credit-packs") {
        packs.push(pack("p2", String(c.body?.name), String(c.body?.credits), String(c.body?.price_minor), Number(c.body?.validity_days ?? 0)));
        return { status: 201, body: packs.at(-1) };
      }
      if (c.method === "POST" && c.path === "/v1/payment-links") {
        return { status: 201, body: { id: "l1", kind: "credit_pack", pack_id: "p1", quantity: 2, credits: "2000", token: "tok", url: "/i/tok", replayed: false } };
      }
      if (c.path === "/v1/credits/settings") return { body: { low_threshold: "0" } };
      if (c.path === "/v1/credit-packs") return { body: { packs } };
      if (c.path === "/v1/customers") return { body: { customers: [ada] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await expect(page.getByRole("row", { name: /Starter/ })).toContainText("30 days");

    const form = page.getByRole("form", { name: "new credit pack" });
    await form.getByLabel("Pack name").fill("Annual");
    await form.getByLabel("Credits", { exact: true }).fill("12000");
    await form.getByLabel("Price").fill("100");
    await form.getByLabel("Valid for (days, optional)").fill("365");
    await form.getByRole("button", { name: "Add pack" }).click();
    await expect(page.getByRole("row", { name: /Annual/ })).toContainText("365 days");
    expect(gw.called("POST", "/v1/credit-packs")[0]?.body).toMatchObject({ name: "Annual", validity_days: 365 });

    await page.getByRole("row", { name: /Starter/ }).getByRole("button", { name: "Create purchase link" }).click();
    const link = page.getByRole("form", { name: "Create purchase link" });
    await link.getByLabel("Customer").selectOption("cus_1");
    await link.getByLabel("Quantity").fill("2");
    await link.getByRole("button", { name: "Create link" }).click();
    await expect(page.getByTestId("purchase-link-url")).toHaveValue(/\/i\/tok$/);
    const body = gw.called("POST", "/v1/payment-links")[0]?.body as Record<string, unknown>;
    expect(body).toMatchObject({ kind: "credit_pack", pack_id: "p1", customer_external_id: "cus_1", quantity: 2 });
    expect(body).not.toHaveProperty("amount_minor");
    expect(body).not.toHaveProperty("currency");
    expect(String(body.idempotency_key)).toMatch(/^[0-9a-f-]{36}$/);
  });

  test("a purchase shows in the customer's ledger, and the low-balance alert is saved", async ({ page }) => {
    let threshold = "0";
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "PUT" && c.path === "/v1/credits/settings") {
        threshold = String(c.body?.low_threshold);
        return { body: { low_threshold: threshold } };
      }
      if (c.path === "/v1/credits/settings") return { body: { low_threshold: threshold } };
      if (c.path === "/v1/credit-packs") return { body: { packs: [] } };
      if (c.path === "/v1/customers") return { body: { customers: [ada] } };
      if (c.path === "/v1/customers/cus_1/credits/ledger")
        return {
          body: {
            entries: [{ id: "e1", lot_id: "l1", kind: "grant", amount: "1000", reason: "credit pack purchase: Starter", source: "purchase", create_time: "2026-10-21T09:05:00Z" }],
            next_page_token: "",
          },
        };
      if (c.path === "/v1/customers/cus_1/credits")
        return {
          body: {
            balance: "1000",
            next_expire_time: "2026-11-20T00:00:00Z",
            lots: [{ id: "l1", granted: "1000", remaining: "1000", source: "purchase", expire_time: "2026-11-20T00:00:00Z", create_time: "2026-10-21T09:05:00Z" }],
          },
        };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await page.getByLabel("Choose a customer").selectOption("cus_1");
    await expect(page.getByRole("row", { name: /Added/ })).toContainText("credit pack purchase: Starter");
    await expect(page.getByRole("row", { name: /Added/ })).toContainText("Purchase");

    const alert = page.getByRole("form", { name: "Low-balance alert" });
    await alert.getByLabel("Alert below (credits)").fill("100");
    await alert.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Saved." })).toBeVisible();
    expect(gw.called("PUT", "/v1/credits/settings")[0]?.body).toEqual({ low_threshold: "100" });
  });
});
