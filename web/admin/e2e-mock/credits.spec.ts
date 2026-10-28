import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { pickPlace, rowAction } from "./ui";

const pack = (id: string, name: string, credits: string, price: string, active = true) => ({ id, name, credits, price_minor: price, currency: "USD", active, create_time: "2026-10-21T09:00:00Z" });

test.describe("credits", () => {
  test("a pack is created from a price typed in major units and withdrawn from sale", async ({ page }) => {
    const packs = [pack("p1", "Starter", "1000", "500")];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "POST" && c.path === "/v1/credit-packs") {
        packs.push(pack("p2", String(c.body?.name), String(c.body?.credits), String(c.body?.price_minor)));
        return { status: 201, body: packs.at(-1) };
      }
      if (c.method === "POST" && c.path === "/v1/credit-packs/p1/deactivate") {
        packs[0] = { ...packs[0]!, active: false };
        return { body: {} };
      }
      if (c.path === "/v1/credit-packs") return { body: { packs } };
      if (c.path === "/v1/customers") return { body: { customers: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await expect(page.getByRole("row", { name: /Starter/ })).toContainText("$5.00");

    await page.getByRole("button", { name: "New credit pack" }).click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Pack name").fill("Team");
    await form.getByLabel("Credits", { exact: true }).fill("10000");
    await form.getByLabel("Price").fill("39,90");
    await form.getByRole("button", { name: "Add pack" }).click();
    await expect(page.getByRole("row", { name: /Team/ })).toContainText("$39.90");
    expect(gw.called("POST", "/v1/credit-packs")[0]?.body).toEqual({ name: "Team", credits: "10000", price_minor: "3990", currency: "USD" });

    await rowAction(page.getByRole("row", { name: /Starter/ }), "Withdraw");
    await expect(page.getByRole("row", { name: /Starter/ })).toContainText("Withdrawn");
  });

  test("a customer's balance, lots and ledger show, and credits are granted with an idempotency key", async ({ page }) => {
    let balance = 300;
    const ledger = [{ id: "e1", lot_id: "l1", kind: "consume", amount: "-120", reason: "ai run", create_time: "2026-10-21T09:05:00Z" }];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs") return { body: { packs: [] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" }] } };
      if (c.path === "/v1/customers/cus_1/credits/grant") {
        balance += Number(c.body?.credits);
        ledger.unshift({ id: "e2", lot_id: "l2", kind: "grant", amount: String(c.body?.credits), reason: String(c.body?.reason ?? ""), create_time: "2026-10-21T09:10:00Z" });
        return { body: { balance: String(balance), replayed: false } };
      }
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: ledger, next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits")
        return { body: { balance: String(balance), next_expire_time: null, lots: [{ id: "l1", granted: "420", remaining: String(balance), source: "promo", expire_time: null, create_time: "2026-10-21T09:00:00Z" }] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await expect(page.getByRole("status", { name: "credits available" })).toContainText("300");
    // The grants the balance comes from are in the first tab, the ledger of everything that happened to it in the second.
    await expect(page.getByRole("row", { name: /Promotion/ })).toContainText("Never");
    await expect(page.getByRole("row", { name: /Spent/ })).toHaveCount(0);
    await page.getByTestId("credits-tab-ledger").click();
    await expect(page.getByRole("row", { name: /Spent/ })).toContainText("-120");

    await page.getByRole("button", { name: "Grant free credits" }).click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Credits to add").fill("50");
    await form.getByLabel("Note (optional)").fill("launch gift");
    await form.getByRole("button", { name: "Add credits" }).click();
    await expect(page.getByRole("status", { name: "credits available" })).toContainText("350");
    await expect(page.getByRole("row", { name: /Added/ })).toContainText("launch gift");
    const body = gw.called("POST", "/v1/customers/cus_1/credits/grant")[0]?.body as Record<string, unknown>;
    expect(body).toMatchObject({ credits: "50", source: "promo", reason: "Welcome gift: launch gift" });
    expect(String(body.idempotency_key)).toMatch(/^[0-9a-f-]{36}$/);
  });

  test("a grant that did not go through is retried with the same idempotency key; the next grant gets a new one", async ({ page }) => {
    let calls = 0;
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs") return { body: { packs: [] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" }] } };
      if (c.path === "/v1/customers/cus_1/credits/grant") {
        calls++;
        // the first try times out at the gateway (it may or may not have been applied)
        return calls === 1 ? { status: 503, body: { error: { code: "UNAVAILABLE", message: "try again" } } } : { body: { balance: "50", replayed: false } };
      }
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [], next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits") return { body: { balance: "0", next_expire_time: null, lots: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await page.getByRole("button", { name: "Grant free credits" }).click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Credits to add").fill("50");
    await form.getByRole("button", { name: "Add credits" }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    await form.getByRole("button", { name: "Add credits" }).click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/credits/grant").length).toBe(2);
    // A grant that went through closes the sheet; it opens clean for the next one.
    await expect(form).toHaveCount(0);
    await page.getByRole("button", { name: "Grant free credits" }).click();
    await expect(form.getByLabel("Credits to add")).toHaveValue("");
    await form.getByLabel("Credits to add").fill("10");
    await form.getByRole("button", { name: "Add credits" }).click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/credits/grant").length).toBe(3);
    const keys = gw.called("POST", "/v1/customers/cus_1/credits/grant").map((c) => String(c.body?.idempotency_key));
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[1]);
  });

  test("a refusal from the API is shown in the reader's language", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs" && c.method === "POST") return { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "billing.credits.unknown_currency", message: "the currency is not supported" } } };
      if (c.path === "/v1/credit-packs") return { body: { packs: [] } };
      if (c.path === "/v1/customers") return { body: { customers: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    await page.getByRole("button", { name: "New credit pack" }).click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Pack name").fill("Odd");
    await form.getByLabel("Credits", { exact: true }).fill("5");
    await form.getByLabel("Price").fill("1");
    await form.getByRole("button", { name: "Add pack" }).click();
    await expect(page.getByRole("alert")).toContainText("This currency is not supported.");
  });

  test("the balance is on top with the grants and the ledger in tabs, and selling credits is a section of its own", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs") return { body: { packs: [pack("p1", "Starter", "1000", "500")] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" }] } };
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [{ id: "e1", lot_id: "l1", kind: "consume", amount: "-120", reason: "ai run", create_time: "2026-10-21T09:05:00Z" }], next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits")
        return { body: { balance: "300", next_expire_time: "2026-12-01T00:00:00Z", lots: [{ id: "l1", granted: "420", remaining: "300", source: "promo", expire_time: null, create_time: "2026-10-21T09:00:00Z" }] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    const balance = page.getByTestId("credits-balance");
    const sell = page.getByTestId("credits-sell");
    await expect(balance).toContainText("Choose a customer to see their balance");
    await expect(sell.getByRole("heading", { name: "Sell credits" })).toBeVisible();
    await expect(sell.getByTestId("credits-packs")).toContainText("Starter");
    await expect(sell.getByTestId("credits-alert")).toBeVisible();
    // The balance comes first on the page.
    const top = async (id: string) => (await page.getByTestId(id).boundingBox())?.y ?? 0;
    expect(await top("credits-balance")).toBeLessThan(await top("credits-sell"));

    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await expect(balance.getByTestId("credits-balance-value")).toHaveText("300");
    await expect(balance).toContainText("Some expire");
    await expect(balance.getByRole("tab", { name: "Grants" })).toHaveAttribute("data-state", "active");
    await balance.getByRole("tab", { name: "Credit history" }).click();
    await expect(page.getByTestId("credits-ledger").getByRole("row", { name: /Spent/ })).toContainText("-120");

  });

  test("the grant sheet and a pack's purchase link come back from the address", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs") return { body: { packs: [pack("p1", "Starter", "1000", "500")] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" }] } };
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [], next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits") return { body: { balance: "300", lots: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    const sheetParam = () => new URL(page.url()).searchParams.get("sheet");

    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await page.getByTestId("credits-grant").click();
    await expect.poll(sheetParam).toBe("grant-credits:cus_1");
    await page.reload();
    // The customer is chosen again from the address, and the sheet is open for it.
    await expect(page.getByRole("dialog").getByText("Free credits go into the balance of cus_1")).toBeVisible();
    await expect(page.getByTestId("credits-balance-value")).toHaveText("300");
    await page.keyboard.press("Escape");
    await expect.poll(sheetParam).toBeNull();

    await rowAction(page.getByRole("row", { name: /Starter/ }), "Create purchase link");
    await expect.poll(sheetParam).toBe("purchase-link:p1");
    await page.reload();
    await expect(page.getByRole("dialog")).toContainText("Starter");
    await page.keyboard.press("Escape");
    await expect.poll(sheetParam).toBeNull();
  });
});

test.describe("credits on a phone", () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test("nothing scrolls sideways, with the customer's grants and ledger open too", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/credit-packs") return { body: { packs: [pack("p1", "Starter", "1000", "500")] } };
      if (c.path === "/v1/customers") return { body: { customers: [{ id: "id-cus_1", external_id: "cus_1", create_time: "2026-10-05T10:00:00Z", name: "Ada", email: "ada@example.com" }] } };
      if (c.path === "/v1/customers/cus_1/credits/ledger") return { body: { entries: [{ id: "e1", lot_id: "l1", kind: "consume", amount: "-120", reason: "ai run", create_time: "2026-10-21T09:05:00Z" }], next_page_token: "" } };
      if (c.path === "/v1/customers/cus_1/credits") return { body: { balance: "300", next_expire_time: "2026-12-01T00:00:00Z", lots: [{ id: "l1", granted: "420", remaining: "300", source: "promo", expire_time: null, create_time: "2026-10-21T09:00:00Z" }] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/credits");
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(await overflow()).toBeLessThanOrEqual(0);
    await pickPlace(page.getByLabel("Choose a customer"), "cus_1");
    await expect(page.getByTestId("credits-balance-value")).toHaveText("300");
    expect(await overflow()).toBeLessThanOrEqual(0);
    await page.getByTestId("credits-tab-ledger").click();
    await expect(page.getByTestId("credits-ledger").getByRole("row", { name: /Spent/ })).toBeVisible();
    expect(await overflow()).toBeLessThanOrEqual(0);
  });
});
