import { expect, test, type Page } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { ACCOUNT, DISPUTES, INVOICE_1, INVOICE_2, LIVE, billingGateway, disputesGateway, invoice, invoiceLines, productGateway, signInAsAda } from "./product";
import { pickFilter } from "./ui";

// Disputes (chargebacks): the card on an invoice's sheet, the Disputes view of the payments page, and the dunning switch that blocks the customer.
test.describe("disputes of the app", () => {
  const open = async (page: Page) => {
    const gw = disputesGateway(billingGateway(productGateway(new MockGateway()).gw));
    // The sheet of the second invoice (the billing mocks only have the first one's lines).
    gw.on((c) => (c.method === "GET" && c.path === `/v1/invoices/${INVOICE_2}` ? { body: invoice(INVOICE_2, "INV-2026-000002", "open", "29900", "cus_globex", { lines: invoiceLines, payments: [] }) } : undefined));
    await gw.install(page);
    await signInAsAda(page);
    return gw;
  };

  test("an invoice's sheet shows its disputes: an open chargeback with the evidence date, a lost one without it", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/invoices`);
    await page.getByTestId("invoice-INV-2026-000003").click();
    const card = page.getByTestId("detail-sheet").getByTestId("dispute-card");
    await expect(card.getByTestId("dispute")).toHaveCount(1);
    await expect(card.getByTestId("dispute-outcome")).toHaveText("Open");
    await expect(card).toContainText("Chargeback");
    await expect(card).toContainText("$49.00");
    await expect(card).toContainText("Fraud claimed");
    await expect(card.getByTestId("dispute-meaning")).toContainText("has taken the money back");
    await expect(card).toContainText("Evidence due");
    expect(gw.called("GET", "/v1/disputes").some((c) => c.query.get("invoice_id") === INVOICE_1)).toBe(true);

    await page.keyboard.press("Escape");
    await page.getByTestId("invoice-INV-2026-000002").click();
    const lost = page.getByTestId("detail-sheet").getByTestId("dispute-card");
    await expect(lost.getByTestId("dispute-outcome")).toHaveText("Lost");
    await expect(lost).toContainText("Paddle");
    await expect(lost).toContainText("Closed");
    await expect(lost).not.toContainText("Evidence due");
  });

  test("the card shows what the dispute cost, and the seller can write a note, mark the case handled and lift the mark", async ({ page }) => {
    const gw = await open(page);
    await page.goto(`/app/t/${LIVE}/invoices`);
    await page.getByTestId("invoice-INV-2026-000003").click();
    const card = page.getByTestId("detail-sheet").getByTestId("dispute-card");
    await expect(card).toContainText("Dispute fee");
    await expect(card).toContainText("$15.00");
    const notes = card.getByTestId("dispute-notes");
    await expect(notes.getByTestId("dispute-events-empty")).toBeVisible();
    await expect(notes.getByTestId("dispute-note-add")).toBeDisabled(); // nothing written

    await notes.getByTestId("dispute-note-input").fill("  Customer signed for the parcel on the 3rd.  ");
    await notes.getByTestId("dispute-note-add").click();
    await expect.poll(() => gw.called("POST", "/v1/disputes/1/notes").length).toBe(1);
    expect(gw.called("POST", "/v1/disputes/1/notes")[0]?.body).toEqual({ note: "Customer signed for the parcel on the 3rd." });
    await expect(notes.getByTestId("dispute-events")).toContainText("Customer signed for the parcel on the 3rd.");
    await expect(notes.getByTestId("dispute-events")).toContainText("ada@example.com");
    await expect(notes.getByTestId("dispute-note-input")).toHaveValue("");

    await expect(card.getByTestId("dispute-handled-pill")).toHaveCount(0);
    await notes.getByTestId("dispute-handled").click();
    await expect.poll(() => gw.called("PUT", "/v1/disputes/1/handled").length).toBe(1);
    expect(gw.called("PUT", "/v1/disputes/1/handled")[0]?.body).toEqual({ handled: true });
    await expect(card.getByTestId("dispute-handled-pill")).toHaveText("Handled");
    await expect(notes.getByTestId("dispute-events").locator('[data-action="handled"]')).toContainText("Marked as handled");
    await expect(notes.getByTestId("dispute-handled")).toHaveText("Lift the handled mark");

    await notes.getByTestId("dispute-handled").click();
    await expect.poll(() => gw.called("PUT", "/v1/disputes/1/handled").length).toBe(2);
    expect(gw.called("PUT", "/v1/disputes/1/handled")[1]?.body).toEqual({ handled: false });
    await expect(card.getByTestId("dispute-handled-pill")).toHaveCount(0);
    await expect(notes.getByTestId("dispute-handled")).toHaveText("Mark as handled");
  });

  test("a case whose evidence is due within three days says so, until it is marked handled", async ({ page }) => {
    const gw = await open(page);
    gw.on((c) => (c.method === "GET" && c.path === "/v1/disputes" && c.query.get("invoice_id") === INVOICE_1 ? { body: { disputes: [{ ...DISPUTES[0], evidence_due_time: "2026-10-22T09:00:00Z" }] } } : undefined));
    await page.goto(`/app/t/${LIVE}/invoices`);
    await page.getByTestId("invoice-INV-2026-000003").click();
    await expect(page.getByTestId("detail-sheet").getByTestId("dispute-card")).toContainText("soon");
  });

  test("a viewer reads a case's notes and is given no form and no button", async ({ page }) => {
    const { gw } = productGateway(new MockGateway(), { role: "viewer", memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role: "viewer" }] });
    disputesGateway(billingGateway(gw), true);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/invoices`);
    await page.getByTestId("invoice-INV-2026-000003").click();
    const notes = page.getByTestId("detail-sheet").getByTestId("dispute-card").getByTestId("dispute-notes");
    await expect(notes.getByTestId("dispute-events-empty")).toBeVisible();
    await expect(notes.getByTestId("dispute-note-input")).toHaveCount(0);
    await expect(notes.getByTestId("dispute-note-add")).toHaveCount(0);
    await expect(notes.getByTestId("dispute-handled")).toHaveCount(0);
  });

  test("the Disputes view lists the cases; outcome applies on its button, search and sort narrow and order, a row opens its invoice", async ({ page }) => {
    await open(page);
    await page.goto(`/app/t/${LIVE}/payments`);
    await page.getByTestId("payments-tab-disputes").click();
    await expect.poll(() => new URL(page.url()).searchParams.get("tab")).toBe("disputes");
    const rows = page.getByTestId("dispute-row");
    await expect(rows).toHaveCount(DISPUTES.length);
    await expect(rows.first()).toContainText("du_1"); // newest first

    await pickFilter(page, "Outcome", "Lost");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("du_2");
    await expect(rows.first()).toContainText("$299.00");
    await page.getByRole("button", { name: "Outcome: ×" }).click();
    await expect(rows).toHaveCount(DISPUTES.length);

    await pickFilter(page, "Type", "Inquiry");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("du_3");
    await page.getByRole("button", { name: "Type: ×" }).click();

    // The seller's own mark: du_3 was marked handled.
    await expect(rows.filter({ hasText: "du_3" }).getByTestId("dispute-handled-pill")).toHaveText("Handled");
    await pickFilter(page, "Handled", "Not handled");
    await expect(rows).toHaveCount(2);
    await page.getByRole("button", { name: "Handled: ×" }).click();
    await pickFilter(page, "Handled", "Handled");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("du_3");
    await page.getByRole("button", { name: "Handled: ×" }).click();
    await expect(rows).toHaveCount(DISPUTES.length);

    await page.getByRole("searchbox").fill("du_2");
    await expect(rows).toHaveCount(1);
    await page.getByRole("searchbox").fill("");
    await page.getByTestId("sort-select").selectOption("oldest");
    await expect(rows.first()).toContainText("du_3");
    await page.getByTestId("sort-select").selectOption("amount_desc");
    await expect(rows.first()).toContainText("du_2");

    await rows.filter({ hasText: "du_1" }).click();
    await expect(page.getByTestId("detail-sheet")).toContainText("INV-2026-000003");
    await expect(page.getByTestId("detail-sheet").getByTestId("dispute-card")).toBeVisible();
  });

  test("the Disputes view says so when there are none", async ({ page }) => {
    const gw = billingGateway(productGateway(new MockGateway()).gw);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/t/${LIVE}/payments?tab=disputes`);
    await expect(page.getByTestId("disputes-empty")).toBeVisible();
  });
});

test.describe("block the customer on a chargeback", () => {
  const settings = (blocked: boolean) => (c: { path: string; method: string }) =>
    c.path === "/v1/dunning-settings" && c.method === "GET" ? { body: { retry_hours: [24, 72, 168], defaults_in_use: false, block_on_chargeback: blocked } } : undefined;
  const echo = (c: { path: string; method: string; body: Record<string, unknown> | null }) => (c.path === "/v1/dunning-settings" && c.method === "PUT" ? { body: c.body } : undefined);
  const taxRates = (c: { path: string; method: string }) => (c.path === "/v1/tax-rates" && c.method === "GET" ? { body: { rates: [] } } : undefined);

  test("the switch is saved with the retries, and sent only when it changed", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(taxRates).on(settings(false)).on(echo);
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    const sw = page.getByTestId("block-on-chargeback");
    await expect(sw).not.toBeChecked();
    await expect(page.getByText("never lifts by itself")).toBeVisible();

    // Untouched: the retries alone are saved and the setting is left as it is.
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/dunning-settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/dunning-settings")[0]?.body).toEqual({ retry_hours: [24, 72, 168] });

    await sw.click();
    await expect(sw).toBeChecked();
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/dunning-settings").length).toBe(2);
    expect(gw.called("PUT", "/v1/dunning-settings")[1]?.body).toEqual({ retry_hours: [24, 72, 168], block_on_chargeback: true });
  });

  test("a stored switch shows on, and turning it off is sent as false", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(taxRates).on(settings(true)).on(echo);
    await gw.install(page);
    await openTenantPage(page, "/pricing");
    const sw = page.getByTestId("block-on-chargeback");
    await expect(sw).toBeChecked();
    await sw.click();
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect.poll(() => gw.called("PUT", "/v1/dunning-settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/dunning-settings")[0]?.body).toEqual({ retry_hours: [24, 72, 168], block_on_chargeback: false });
  });
});
