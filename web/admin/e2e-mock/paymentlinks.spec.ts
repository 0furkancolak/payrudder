import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { rowAction } from "./ui";

const link = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  kind: "adhoc",
  status: "active",
  single_use: true,
  amount_minor: "1250",
  currency: "USD",
  description: "Setup fee",
  create_time: "2026-10-20T10:00:00Z",
  expire_time: "2026-11-19T10:00:00Z",
  paid_count: 0,
  use_count: 0,
  ...extra,
});

test.describe("payment links", () => {
  test("an ad-hoc link is created with minor units and an idempotency key, its URL shown once, then revoked", async ({ page }) => {
    const links = [link("l1")];
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.path === "/v1/payment-links" && c.method === "POST") {
        links.push(link("l2", { description: String(c.body?.description) }));
        return { status: 201, body: { ...links.at(-1), token: "tok", url: "https://api.example.com/i/tok", replayed: false } };
      }
      if (c.path === "/v1/payment-links" && c.method === "GET") return { body: { links } };
      if (c.path === "/v1/payment-links/l2" && c.method === "DELETE") {
        links[1] = link("l2", { status: "revoked", description: "Workshop" });
        return { body: links[1] };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/payment-links");
    await expect(page.getByRole("cell", { name: /Setup fee/ })).toBeVisible();

    await page.getByRole("button", { name: "Create link" }).click();
    const form = page.getByRole("dialog");
    await form.getByLabel("Amount").fill("0");
    await form.getByLabel("What it is for").fill("Workshop");
    await form.getByRole("button", { name: "Create link" }).click();
    await expect(page.getByRole("alert")).toContainText("positive amount");
    expect(gw.called("POST", "/v1/payment-links")).toHaveLength(0);

    await form.getByLabel("Amount").fill("49.90");
    await form.getByLabel("Promotion code (optional)").fill("WELCOME");
    await form.getByRole("button", { name: "Create link" }).click();
    await expect(page.getByTestId("payment-link-url")).toHaveValue("https://api.example.com/i/tok");
    await expect(page.getByRole("status")).toContainText("shown once");
    const body = gw.called("POST", "/v1/payment-links")[0]?.body;
    expect(body).toMatchObject({ kind: "adhoc", amount_minor: "4990", currency: "USD", description: "Workshop", single_use: true, promotion_code: "WELCOME" });
    expect(String(body?.idempotency_key).length).toBeGreaterThan(10);
    await page.keyboard.press("Escape");

    page.once("dialog", (d) => void d.accept());
    await rowAction(page.getByRole("row", { name: /Workshop/ }), "Revoke");
    await expect.poll(() => gw.called("DELETE", "/v1/payment-links/l2").length).toBe(1);
    await expect(page.getByRole("row", { name: /Workshop/ })).toContainText("Revoked");
  });

  test("the invoice page offers the payment links page for that invoice", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/v1/payment-links") return { body: { links: [] } };
      if (c.method === "GET" && c.path.endsWith("/invoices/inv1")) return { body: { id: "inv1", number: "INV-1", status: "open", currency: "USD", total_minor: "100", lines: [] } };
      if (c.method === "GET" && c.path.endsWith("/invoices/inv1/saga")) return { body: { state: "AWAITING_PAYMENT", events: [] } };
      if (c.method === "GET" && c.path === "/v1/credit-notes") return { body: { credit_notes: [] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("link", { name: "Payment link", exact: true }).click();
    await expect(page.getByRole("dialog").getByLabel("Invoice id")).toHaveValue("inv1");
  });

  test("a customer's payment page link is made and revoked, and their payments are listed", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method === "GET" && c.path === "/v1/customers") return { body: { customers: [{ id: "id-1", external_id: "cus_1", name: "Ada", email: "a@example.com" }] } };
      if (c.method === "POST" && c.path === "/v1/customers/cus_1/portal-links") return { status: 201, body: { token: "t", url: "/c/t", expires_at: "2026-11-19T10:00:00Z", customer_id: "id-1" } };
      if (c.method === "DELETE" && c.path === "/v1/customers/cus_1/portal-links") return { body: { revoked: 1 } };
      if (c.method === "GET" && c.path === "/v1/customers/cus_1/payments")
        return { body: { invoices: [], payments: [{ invoice_id: "inv1", invoice_number: "INV-1", kind: "charge", status: "succeeded", amount_minor: "1250", currency: "USD", provider: "mockpsp", occurred_at: "2026-10-20T10:00:00Z" }] } };
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("row-actions").click();
    await page.getByTestId("row-action-portal").click();
    const card = page.getByRole("region", { name: "Payment page of cus_1" });
    await expect(card.getByRole("cell", { name: "INV-1" })).toBeVisible();
    await card.getByLabel("Valid for (days)").fill("14");
    await card.getByRole("button", { name: "Make a link" }).click();
    await expect(card.getByTestId("portal-link-url")).toHaveValue(/\/c\/t$/);
    expect(gw.called("POST", "/v1/customers/cus_1/portal-links")[0]?.body).toEqual({ expires_in_days: 14 });
    page.once("dialog", (d) => void d.accept());
    await card.getByRole("button", { name: "Revoke all" }).click();
    await expect.poll(() => gw.called("DELETE", "/v1/customers/cus_1/portal-links").length).toBe(1);
  });
});
