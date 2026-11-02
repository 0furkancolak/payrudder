import { expect, test } from "@playwright/test";
import { acceptance, contractsGateway, invoice, SNAPSHOT_HTML, STARTER_EN, version, withdrawal } from "./contracts";
import { baseTenant, MockGateway, openTenantPage } from "./gateway";
import { appWithLive, APP, liveGateway, LIVE, productGateway, signInAsAda } from "./product";
import { pickFilters } from "./ui";

// Contracts (docs/19-contracts.md): the seller's agreements, what it requires and the withdrawals that come back, and what a customer's sheet shows of them.
// The gateway is mocked: the preview finds unknown variables the way the backend does, and versions only grow.

test.use({ viewport: { width: 1440, height: 900 } });

const TOS = "tos~consumer";

async function open(page: import("@playwright/test").Page, state: Parameters<typeof contractsGateway>[1] = {}, path = "/contracts") {
  const gw = baseTenant(new MockGateway());
  const contracts = contractsGateway(gw, state);
  await gw.install(page);
  await openTenantPage(page, path);
  return { gw, contracts };
}

test.describe("agreements", () => {
  test("a first version is published from the starter text: the request carries the text, material is on, nothing in it is unknown", async ({ page }) => {
    const { gw } = await open(page);
    await expect(page.getByTestId("contracts-disclaimer")).toContainText("not legal advice");
    await expect(page.getByTestId("contracts-empty")).toBeVisible();
    await expect(page.getByTestId(`agreement-publish-${TOS}`)).toHaveText("Publish first version");
    await page.getByTestId(`agreement-publish-${TOS}`).click();

    // The language tabs: a text is written in each language the seller sells in, the starter text exists in two of them.
    await expect(page.getByTestId("version-sheet")).toBeVisible();
    await page.getByTestId("version-lang-de").click();
    await expect(page.getByTestId("version-starter")).toBeDisabled();
    await expect(page.getByTestId("version-starter-only")).toContainText("Turkish and English only");
    await page.getByTestId("version-lang-en").click();
    await page.getByTestId("version-starter").click();
    await expect(page.getByTestId("version-text")).toHaveValue(STARTER_EN);

    // Nothing is published until the text has been checked: the preview is a sandboxed frame and the starter notice is called out.
    await expect(page.getByTestId("version-notice")).toContainText("starter notice");
    const frame = page.getByTestId("version-preview").locator("iframe");
    await expect(frame).toHaveAttribute("sandbox", "");
    await expect(frame).toHaveAttribute("srcdoc", /<mark>sample seller_name<\/mark>/);
    await expect(page.getByTestId("version-unknown")).toHaveCount(0);
    expect(gw.called("POST", "/v1/contracts/preview")[0]?.body).toEqual({ body: STARTER_EN, locale: "en" });
    // The first version is always material, and the switch says so.
    await expect(page.getByTestId("version-material")).toBeChecked();
    await expect(page.getByTestId("version-material")).toBeDisabled();

    await expect(page.getByTestId("version-publish")).toBeEnabled();
    await page.getByTestId("version-publish").click();
    // What publishing does is said before it is done.
    const confirm = page.getByTestId("publish-confirm");
    await expect(confirm).toContainText("cannot be edited");
    await expect(confirm.locator('[data-fact="first"]')).toBeVisible();
    await expect(confirm.locator('[data-fact="notRequired"]')).toBeVisible();
    await expect(confirm.locator('[data-fact="template"]')).toBeVisible();
    expect(gw.called("POST", "/v1/contracts/versions")).toHaveLength(0);
    await page.getByTestId("publish-confirm-accept").click();

    await expect.poll(() => gw.called("POST", "/v1/contracts/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/contracts/versions")[0]?.body).toEqual({ kind: "tos", audience: "consumer", bodies: { en: STARTER_EN }, material: true, note: "" });
    // The page now shows version 1 with the warning that it is a starter text.
    const row = page.getByTestId(`agreement-${TOS}`);
    await expect(row.getByTestId("agreement-number")).toHaveText("Version 1");
    await expect(row.getByTestId("agreement-template")).toBeVisible();
    await expect(page.getByTestId("contracts-template-banner")).toContainText("have a lawyer review it");
    await expect(page.getByTestId("contracts-template-banner")).toContainText("not legal advice");
    await expect(page.getByTestId("version-sheet")).toHaveCount(0);
  });

  test("a variable that does not exist blocks publishing until it is gone, and the palette puts a variable at the cursor", async ({ page }) => {
    const { gw } = await open(page);
    await page.getByTestId(`agreement-publish-${TOS}`).click();
    const text = page.getByTestId("version-text");

    await text.fill("Hello , bye");
    await text.evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.setSelectionRange(6, 6);
    });
    await page.getByTestId("variable-customer_name").click();
    await expect(text).toHaveValue("Hello {{customer_name}}, bye");
    await expect(page.getByTestId("version-publish")).toBeEnabled();

    await text.fill("Hello {{nope}} and {{customer_name}}");
    await expect(page.getByTestId("version-unknown")).toContainText("nope is not a variable");
    await expect(page.getByTestId("version-publish")).toBeDisabled();
    await text.fill("Hello {{customer_name}}");
    await expect(page.getByTestId("version-unknown")).toHaveCount(0);
    await expect(page.getByTestId("version-publish")).toBeEnabled();
    expect(gw.called("POST", "/v1/contracts/versions")).toHaveLength(0);
  });

  test("a new version starts from the current one, says what it does to customers who accepted, and sends the choice", async ({ page }) => {
    const { gw, contracts } = await open(page, {
      versions: [version("tos", "consumer", 1, { en: "Old text", tr: "Eski metin" })],
      settings: { required_kinds: ["tos"], immediate_start: false, retention_months: 36, withdrawal_refund: "full", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true },
    });
    await expect(page.getByTestId(`agreement-${TOS}`).getByTestId("agreement-required")).toBeVisible();
    await page.getByTestId(`agreement-publish-${TOS}`).click();
    await expect(page.getByTestId("version-text")).toHaveValue("Old text");
    await page.getByTestId("version-text").fill("New text");
    await page.getByTestId("version-note").fill("Clarified the refund terms");
    // Later versions are material by default; the seller can say they are not.
    await expect(page.getByTestId("version-material")).toBeEnabled();
    await expect(page.getByTestId("version-material")).toBeChecked();
    await page.getByTestId("version-publish").click();
    const facts = page.getByTestId("publish-facts");
    await expect(facts.locator('[data-fact="material"]')).toContainText("asked to accept this one at their next purchase");
    await expect(facts.locator('[data-fact="required"]')).toContainText("customers can only pay once they have accepted it");
    await page.getByRole("button", { name: "Cancel" }).last().click();
    await page.getByTestId("version-material").click();
    await page.getByTestId("version-publish").click();
    await expect(facts.locator('[data-fact="minor"]')).toContainText("not asked again");
    await expect(facts.locator('[data-fact="material"]')).toHaveCount(0);
    await page.getByTestId("publish-confirm-accept").click();

    await expect.poll(() => gw.called("POST", "/v1/contracts/versions").length).toBe(1);
    expect(gw.called("POST", "/v1/contracts/versions")[0]?.body).toEqual({ kind: "tos", audience: "consumer", bodies: { en: "New text", tr: "Eski metin" }, material: false, note: "Clarified the refund terms" });
    expect(contracts.versions.filter((v) => v.current).map((v) => v.number)).toEqual([2]);
    await expect(page.getByTestId(`agreement-${TOS}`).getByTestId("agreement-number")).toHaveText("Version 2");
  });

  test("the editor and the history are in the address: a reload brings them back once the versions have loaded", async ({ page }) => {
    await open(page, { versions: [version("tos", "consumer", 1, { en: "Old text" })] });
    await page.getByTestId(`agreement-publish-${TOS}`).click();
    await expect(page.getByTestId("version-text")).toHaveValue("Old text");
    await expect.poll(() => new URL(page.url()).searchParams.get("sheet")).toBe(`publish-agreement:${TOS}`);
    await page.reload();
    await expect(page.getByTestId("version-sheet")).toBeVisible();
    await expect(page.getByTestId("version-text")).toHaveValue("Old text");
    await page.getByRole("button", { name: "Close" }).first().click();
    await expect(page.getByTestId("version-sheet")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("sheet")).toBeNull();
    await page.getByTestId(`agreement-history-${TOS}`).click();
    await expect.poll(() => new URL(page.url()).searchParams.get("sheet")).toBe(`agreement-history:${TOS}`);
    await page.reload();
    await expect(page.getByTestId("history-sheet")).toBeVisible();
  });

  test("the history lists every version and shows what changed between two of them, line by line", async ({ page }) => {
    await open(page, { versions: [version("tos", "consumer", 1, { en: "Intro\nPay in 14 days\nThanks" }), version("tos", "consumer", 2, { en: "Intro\nPay in 30 days\nThanks" }, { note: "Longer terms" })] });
    await page.getByTestId(`agreement-history-${TOS}`).click();
    const sheet = page.getByTestId("history-sheet");
    await expect(sheet.getByTestId("history-version-2")).toContainText("Current");
    await expect(sheet.getByTestId("history-version-2")).toContainText("Longer terms");
    await expect(sheet.getByTestId("history-version-1")).toContainText("Material");
    // It opens on the newest version against the one before it.
    await expect(sheet.getByTestId("history-from")).toHaveValue("1");
    await expect(sheet.getByTestId("history-to")).toHaveValue("2");
    await expect(sheet.locator('[data-op="remove"]')).toHaveText(/Pay in 14 days/);
    await expect(sheet.locator('[data-op="add"]')).toHaveText(/Pay in 30 days/);
    await expect(sheet.locator('[data-op="same"]')).toHaveCount(2);
    await expect(sheet.getByTestId("history-diff-stats")).toHaveText("Lines added: 1, lines removed: 1");
    await sheet.getByTestId("history-from").selectOption("2");
    await expect(sheet.getByTestId("history-diff-stats")).toContainText("same");
  });
});

// What the settings form sends besides the required kinds, the switch and the retention, while the new settings are at their defaults.
const saved = { withdrawal_refund: "full", renewal_notice_days: 7, renewal_cancel_texts: { en: "", tr: "", de: "", fr: "", es: "", ar: "" }, withdrawal_function: true };

test.describe("settings", () => {
  test("a kind with no published version is disabled with the reason, and the save sends the required kinds, the switch and the retention together", async ({ page }) => {
    const { gw, contracts } = await open(page, { versions: [version("tos", "consumer", 1, { en: "Terms" })] }, "/contracts?tab=settings");
    await expect(page.getByTestId("required-privacy")).toBeDisabled();
    await expect(page.getByTestId("required-privacy-reason")).toContainText("Publish a version first");
    await expect(page.getByTestId("required-tos")).toBeEnabled();
    await expect(page.getByTestId("contracts-retention")).toHaveValue("36");
    await expect(page.getByTestId("contracts-save")).toBeDisabled();

    await page.getByTestId("required-tos").click();
    await page.getByTestId("contracts-immediate").click();
    await page.getByTestId("contracts-retention").fill("60");
    await page.getByTestId("contracts-save").click();
    await expect.poll(() => gw.called("PUT", "/v1/contracts/settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/contracts/settings")[0]?.body).toEqual({ ...saved, required_kinds: ["tos"], immediate_start: true, retention_months: 60 });
    expect(contracts.settings).toEqual({ required_kinds: ["tos"], immediate_start: true, retention_months: 60, withdrawal_refund: "full", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true });
    await expect(page.getByTestId("contracts-saved")).toBeVisible();

    // A retention the API would refuse is caught before it is sent, and a change after the save takes the "saved" away.
    await page.getByTestId("contracts-retention").fill("6");
    await expect(page.getByTestId("contracts-saved")).toHaveCount(0);
    await expect(page.getByRole("alert").filter({ hasText: "from 12 to 120" })).toBeVisible();
    await expect(page.getByTestId("contracts-save")).toBeDisabled();
    // A required kind can be switched off again.
    await page.getByTestId("contracts-retention").fill("36");
    await page.getByTestId("required-tos").click();
    await page.getByTestId("contracts-save").click();
    await expect.poll(() => gw.called("PUT", "/v1/contracts/settings").length).toBe(2);
    expect(gw.called("PUT", "/v1/contracts/settings")[1]?.body).toEqual({ ...saved, required_kinds: [], immediate_start: true, retention_months: 36 });
  });

  test("the withdrawal and renewal-notice settings start at the API's defaults and are sent with the rest", async ({ page }) => {
    const { gw, contracts } = await open(page, { versions: [version("tos", "consumer", 1, { en: "Terms" })] }, "/contracts?tab=settings");
    await expect(page.getByTestId("contracts-refund")).toBeChecked();
    await expect(page.getByTestId("contracts-function")).toBeChecked();
    await expect(page.getByTestId("contracts-notice-days")).toHaveValue("7");
    await expect(page.getByTestId("contracts-cancel-tr")).toHaveValue("");

    await page.getByTestId("contracts-refund").click();
    await page.getByTestId("contracts-function").click();
    await page.getByTestId("contracts-notice-days").fill("10");
    await page.getByTestId("contracts-cancel-tr").fill("Panelden iptal edin.");
    await page.getByTestId("contracts-cancel-ar").fill("ألغِ من اللوحة.");
    await page.getByTestId("contracts-save").click();
    await expect.poll(() => gw.called("PUT", "/v1/contracts/settings").length).toBe(1);
    expect(gw.called("PUT", "/v1/contracts/settings")[0]?.body).toEqual({
      required_kinds: [],
      immediate_start: false,
      retention_months: 36,
      withdrawal_refund: "none",
      renewal_notice_days: 10,
      renewal_cancel_texts: { en: "", tr: "Panelden iptal edin.", de: "", fr: "", es: "", ar: "ألغِ من اللوحة." },
      withdrawal_function: false,
    });
    expect(contracts.settings.renewal_cancel_texts).toEqual({ tr: "Panelden iptal edin.", ar: "ألغِ من اللوحة." });
    await expect(page.getByTestId("contracts-saved")).toBeVisible();

    // A number of days the API would refuse is caught before it is sent; 0 is allowed and switches the notice off.
    await page.getByTestId("contracts-notice-days").fill("61");
    await expect(page.getByRole("alert").filter({ hasText: "from 0 to 60" })).toBeVisible();
    await expect(page.getByTestId("contracts-save")).toBeDisabled();
    await page.getByTestId("contracts-notice-days").fill("0");
    await page.getByTestId("contracts-cancel-tr").fill("");
    await page.getByTestId("contracts-save").click();
    await expect.poll(() => gw.called("PUT", "/v1/contracts/settings").length).toBe(2);
    expect(gw.called("PUT", "/v1/contracts/settings")[1]?.body).toMatchObject({ renewal_notice_days: 0, renewal_cancel_texts: { tr: "" } });
    expect(contracts.settings.renewal_cancel_texts).toEqual({ ar: "ألغِ من اللوحة." });
  });
});

test.describe("withdrawals", () => {
  const rows = () => [
    withdrawal("w1", "cus_1", "received", "I want to cancel my order"),
    withdrawal("w2", "cus_2", "accepted", "Please refund me", { decide_time: "2026-10-19T09:00:00Z", decided_by: "Ada Lovelace", decision_note: "Agreed" }),
    withdrawal("w3", "cus_3", "rejected", "Changed my mind after 30 days", { decide_time: "2026-10-19T10:00:00Z", decided_by: "Ada Lovelace", decision_note: "Out of time" }),
  ];

  test("a received request is decided once, an accepted one is marked refunded, and the page says the refund is the seller's", async ({ page }) => {
    const { gw, contracts } = await open(page, { withdrawals: rows() }, "/contracts?tab=withdrawals");
    await expect(page.getByTestId("withdrawals-note")).toContainText("also asks billing to refund it to the card when your settings say so");
    await expect(page.getByTestId("withdrawal-w1").getByTestId("withdrawal-status")).toHaveText("Received");
    // Only the moves that are allowed are offered.
    await expect(page.getByTestId("withdrawal-accepted-w1")).toBeVisible();
    await expect(page.getByTestId("withdrawal-rejected-w1")).toBeVisible();
    await expect(page.getByTestId("withdrawal-refunded-w1")).toHaveCount(0);
    await expect(page.getByTestId("withdrawal-refunded-w2")).toBeVisible();
    await expect(page.getByTestId("withdrawal-accepted-w2")).toHaveCount(0);
    await expect(page.getByTestId("withdrawal-w3").getByRole("button")).toHaveCount(0);

    await page.getByTestId("withdrawal-accepted-w1").click();
    await expect(page.getByTestId("withdrawal-dialog")).toContainText("cannot be undone");
    await page.getByTestId("withdrawal-note").fill("Within 14 days");
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/w1/status").length).toBe(1);
    expect(gw.called("PUT", "/v1/contract-withdrawals/w1/status")[0]?.body).toEqual({ status: "accepted", note: "Within 14 days" });
    await expect(page.getByTestId("withdrawal-w1").getByTestId("withdrawal-status")).toHaveText("Accepted");
    await expect(page.getByTestId("withdrawal-w1")).toContainText("Within 14 days");
    // It cannot be decided twice: the accept and reject buttons are gone and only the refund is left.
    await expect(page.getByTestId("withdrawal-accepted-w1")).toHaveCount(0);
    await expect(page.getByTestId("withdrawal-rejected-w1")).toHaveCount(0);

    await page.getByTestId("withdrawal-refunded-w1").click();
    await expect(page.getByTestId("withdrawal-dialog")).toContainText("Payrudder does not move any money");
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/w1/status").length).toBe(2);
    expect(gw.called("PUT", "/v1/contract-withdrawals/w1/status")[1]?.body).toEqual({ status: "refunded", note: "" });
    expect(contracts.withdrawals.find((w) => w.id === "w1")?.status).toBe("refunded");
    await expect(page.getByTestId("withdrawal-w1").getByRole("button")).toHaveCount(0);
  });

  test("a rejection is final, and the list narrows to one status when the filter is applied", async ({ page }) => {
    const { gw } = await open(page, { withdrawals: rows() }, "/contracts?tab=withdrawals");
    await page.getByTestId("withdrawal-rejected-w1").click();
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/w1/status").length).toBe(1);
    expect(gw.called("PUT", "/v1/contract-withdrawals/w1/status")[0]?.body).toEqual({ status: "rejected", note: "" });
    await expect(page.getByTestId("withdrawal-w1").getByRole("button")).toHaveCount(0);

    await pickFilters(page, "Status", "Rejected");
    await expect(page.getByTestId("withdrawal-w3")).toBeVisible();
    await expect(page.getByTestId("withdrawal-w1")).toBeVisible();
    await expect(page.getByTestId("withdrawal-w2")).toHaveCount(0);
  });

  test("accepting a withdrawal that names an invoice asks billing for the refund, says so first, and shows what became of it", async ({ page }) => {
    const { gw, contracts } = await open(page, { withdrawals: [withdrawal("wi", "cus_1", "received", "Refund me", { invoice_id: "inv_card" }), withdrawal("wt", "cus_2", "received", "Refund me", { invoice_id: "inv_transfer" }), withdrawal("wn", "cus_3", "received", "No invoice named")] }, "/contracts?tab=withdrawals");
    await page.getByTestId("withdrawal-accepted-wi").click();
    await expect(page.getByTestId("withdrawal-refund-effect")).toContainText("also ask billing to refund the invoice this withdrawal names");
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/wi/status").length).toBe(1);
    // The decision is the only thing the console sends: the refund follows from the setting, on the server.
    expect(gw.called("PUT", "/v1/contract-withdrawals/wi/status")[0]?.body).toEqual({ status: "accepted", note: "" });
    await expect(page.getByTestId("withdrawal-wi").getByTestId("withdrawal-refund-state")).toHaveText("Requested");
    await expect(page.getByTestId("withdrawal-wi").getByTestId("withdrawal-credit-note")).toContainText("cn-wi");
    await expect(page.getByTestId("withdrawal-refund-wi")).toHaveCount(0);

    // A payment made by bank transfer cannot go back through Payrudder: the row says to refund it by hand and says why.
    await page.getByTestId("withdrawal-accepted-wt").click();
    await page.getByTestId("withdrawal-confirm").click();
    await expect(page.getByTestId("withdrawal-wt").getByTestId("withdrawal-refund-state")).toHaveText("Refund it yourself");
    await expect(page.getByTestId("withdrawal-wt")).toContainText("paid by bank transfer");
    await expect(page.getByTestId("withdrawal-refund-wt")).toHaveCount(0);

    // No invoice named: nothing is asked of billing, and the dialog says so.
    await page.getByTestId("withdrawal-accepted-wn").click();
    await expect(page.getByTestId("withdrawal-refund-effect")).toContainText("names no invoice");
    await page.getByTestId("withdrawal-confirm").click();
    await expect(page.getByTestId("withdrawal-wn").getByTestId("withdrawal-refund-state")).toHaveText("Not asked for");
    await expect(page.getByTestId("withdrawal-refund-wn")).toHaveCount(0);
    expect(contracts.withdrawals.find((w) => w.id === "wn")?.credit_note_id).toBe("");
    expect(gw.called("POST", "/v1/contract-withdrawals/wi/refund")).toHaveLength(0);
  });

  test("with the setting none, accepting asks nothing of billing and the seller asks for the refund when it wants to", async ({ page }) => {
    const { gw } = await open(
      page,
      {
        withdrawals: [withdrawal("wi", "cus_1", "received", "Refund me", { invoice_id: "inv_card" })],
        settings: { required_kinds: [], immediate_start: false, retention_months: 36, withdrawal_refund: "none", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true },
      },
      "/contracts?tab=withdrawals",
    );
    await page.getByTestId("withdrawal-accepted-wi").click();
    await expect(page.getByTestId("withdrawal-refund-effect")).toContainText("leave the refund to you");
    await page.getByTestId("withdrawal-confirm").click();
    await expect(page.getByTestId("withdrawal-wi").getByTestId("withdrawal-refund-state")).toHaveText("Not asked for");

    await page.getByTestId("withdrawal-refund-wi").click();
    await expect(page.getByTestId("withdrawal-dialog")).toContainText("never refunds more than the invoice");
    await expect(page.getByTestId("withdrawal-note")).toHaveCount(0);
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("POST", "/v1/contract-withdrawals/wi/refund").length).toBe(1);
    await expect(page.getByTestId("withdrawal-wi").getByTestId("withdrawal-refund-state")).toHaveText("Requested");
    await expect(page.getByTestId("withdrawal-refund-wi")).toHaveCount(0);
    // The refund is asked for once: a second request would be refused, and the button is gone.
    expect(gw.called("POST", "/v1/contract-withdrawals/wi/refund")).toHaveLength(1);
  });

  test("a refund that failed can be asked for again", async ({ page }) => {
    const { gw } = await open(page, { withdrawals: [withdrawal("wf", "cus_1", "accepted", "Refund me", { invoice_id: "inv_card", refund_state: "failed", refund_note: "billing did not answer", decide_time: "2026-10-19T09:00:00Z" })] }, "/contracts?tab=withdrawals");
    await expect(page.getByTestId("withdrawal-wf").getByTestId("withdrawal-refund-state")).toHaveText("Failed");
    await expect(page.getByTestId("withdrawal-wf")).toContainText("billing did not answer");
    await page.getByTestId("withdrawal-refund-wf").click();
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("POST", "/v1/contract-withdrawals/wf/refund").length).toBe(1);
    await expect(page.getByTestId("withdrawal-wf").getByTestId("withdrawal-refund-state")).toHaveText("Requested");
  });
});

test.describe("withdrawals: the invoice, the amount and the result of the card refund", () => {
  const BIG = [invoice("inv_a", "cus_1", "4900")];

  test("accepting shows the invoice, an amount that is not valid or is more than the invoice blocks it, a smaller one is sent in minor units, and empty sends none", async ({ page }) => {
    const { gw } = await open(page, { withdrawals: [withdrawal("wa", "cus_1", "received", "Refund part", { invoice_id: "inv_a" }), withdrawal("wb", "cus_1", "received", "Refund all", { invoice_id: "inv_a" })], invoices: BIG }, "/contracts?tab=withdrawals");
    await page.getByTestId("withdrawal-accepted-wa").click();
    await expect(page.getByTestId("withdrawal-invoice-line")).toContainText("INV-inv_a");
    await expect(page.getByTestId("withdrawal-invoice-line")).toContainText("$49.00");
    const confirm = page.getByTestId("withdrawal-confirm");
    await page.getByTestId("withdrawal-amount").fill("50");
    await expect(page.getByTestId("withdrawal-dialog")).toContainText("That is more than the invoice.");
    await expect(confirm).toBeDisabled();
    await page.getByTestId("withdrawal-amount").fill("abc");
    await expect(page.getByTestId("withdrawal-dialog")).toContainText("Type an amount in this currency");
    await expect(confirm).toBeDisabled();
    await page.getByTestId("withdrawal-amount").fill("12.50");
    await expect(confirm).toBeEnabled();
    await confirm.click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/wa/status").length).toBe(1);
    expect(gw.called("PUT", "/v1/contract-withdrawals/wa/status")[0]?.body).toEqual({ status: "accepted", note: "", refund_amount_minor: "1250" });

    // Left empty, everything that is left of the invoice is refunded: the console names no amount.
    await page.getByTestId("withdrawal-accepted-wb").click();
    await expect(page.getByTestId("withdrawal-amount")).toHaveValue("");
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("PUT", "/v1/contract-withdrawals/wb/status").length).toBe(1);
    expect(gw.called("PUT", "/v1/contract-withdrawals/wb/status")[0]?.body).toEqual({ status: "accepted", note: "" });
  });

  test("with the setting none the dialog does not ask for an amount, and asking for the refund later does", async ({ page }) => {
    const { gw } = await open(
      page,
      {
        withdrawals: [withdrawal("wi", "cus_1", "received", "Refund me", { invoice_id: "inv_a" })],
        invoices: BIG,
        settings: { required_kinds: [], immediate_start: false, retention_months: 36, withdrawal_refund: "none", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true },
      },
      "/contracts?tab=withdrawals",
    );
    await page.getByTestId("withdrawal-accepted-wi").click();
    await expect(page.getByTestId("withdrawal-amount")).toHaveCount(0);
    await page.getByTestId("withdrawal-confirm").click();
    await page.getByTestId("withdrawal-refund-wi").click();
    await page.getByTestId("withdrawal-amount").fill("10");
    await page.getByTestId("withdrawal-confirm").click();
    await expect.poll(() => gw.called("POST", "/v1/contract-withdrawals/wi/refund").length).toBe(1);
    expect(gw.called("POST", "/v1/contract-withdrawals/wi/refund")[0]?.body).toEqual({ amount_minor: "1000" });
  });

  test("the result of the card refund is on the row: refunded, or failed with the reason and where to retry it, and neither can be asked for again", async ({ page }) => {
    await open(
      page,
      {
        withdrawals: [
          withdrawal("wr", "cus_1", "accepted", "Done", { invoice_id: "inv_a", refund_state: "refunded", credit_note_id: "cn-1", decide_time: "2026-10-19T09:00:00Z" }),
          withdrawal("wf", "cus_2", "accepted", "Declined", { invoice_id: "inv_a", refund_state: "refund_failed", refund_note: "psp_declined: the card is closed", credit_note_id: "cn-2", decide_time: "2026-10-19T09:00:00Z" }),
        ],
      },
      "/contracts?tab=withdrawals",
    );
    await expect(page.getByTestId("withdrawal-wr").getByTestId("withdrawal-refund-state")).toHaveText("Refunded");
    await expect(page.getByTestId("withdrawal-wf").getByTestId("withdrawal-refund-state")).toHaveText("Card refund failed");
    await expect(page.getByTestId("withdrawal-wf")).toContainText("psp_declined: the card is closed");
    await expect(page.getByTestId("withdrawal-wf").getByTestId("withdrawal-refund-retry-hint")).toContainText("do not ask for a second one");
    await expect(page.getByTestId("withdrawal-wr").getByTestId("withdrawal-refund-retry-hint")).toHaveCount(0);
    await expect(page.getByTestId("withdrawal-refund-wr")).toHaveCount(0);
    await expect(page.getByTestId("withdrawal-refund-wf")).toHaveCount(0);
  });
});

test.describe("a customer's sheet", () => {
  const customers = [{ id: "id-1", external_id: "cus_1", name: "Ada Lovelace", email: "ada@example.com", country: "TR", billing_currency: "TRY", create_time: "2026-10-01T10:00:00Z" }];
  const log = () => [
    acceptance("a1", "g-new", "distance_sales", "2026-10-15T10:00:00Z", { version_number: 2, locale_requested: "en", locale_served: "tr", ip: "", observed: { ip: true, user_agent: true, time: true, text: true }, immediate_start_consent: true, immediate_start_text: "I ask for the service to start now", withdrawal_waiver_acknowledged: true, withdrawal_waiver_text: "I know I lose the right of withdrawal" }),
    acceptance("a2", "g-new", "pre_info", "2026-10-15T10:00:00Z", { ip: "", immediate_start_consent: true, immediate_start_text: "I ask for the service to start now", withdrawal_waiver_acknowledged: true, withdrawal_waiver_text: "I know I lose the right of withdrawal" }),
    acceptance("a3", "g-old", "tos", "2026-09-01T10:00:00Z", { source: "api", context_kind: "api", ip: "", user_agent: "", observed: { ip: false, user_agent: false, time: false, text: false } }),
  ];

  async function openSheet(page: import("@playwright/test").Page, extra: Parameters<typeof contractsGateway>[1] = {}) {
    const gw = baseTenant(new MockGateway()).on((c) => {
      if (c.method !== "GET") return undefined;
      if (c.path === "/v1/customers") return { body: { customers } };
      if (c.path === "/v1/invoices") return { body: { invoices: [] } };
      if (c.path === "/v1/payments") return { body: { payments: [] } };
      if (c.path === "/v1/subscriptions") return { body: { subscriptions: [] } };
      if (/^\/v1\/customers\/cus_1\/credits$/.test(c.path)) return { body: { balance: "0", lots: [] } };
      return undefined;
    });
    const contracts = contractsGateway(gw, { acceptances: log(), withdrawals: [withdrawal("w9", "cus_1", "received", "Mail from the customer", { source: "api" })], ...extra });
    await gw.install(page);
    await openTenantPage(page, "/customers");
    await page.getByTestId("customer-cus_1").click();
    await page.getByTestId("customer-tab-agreements").click();
    return { gw, contracts };
  }

  test("shows what was accepted together, in which language, who vouches for the evidence, the two consents, and what was erased", async ({ page }) => {
    await openSheet(page);
    const groups = page.getByTestId("acceptance-group");
    await expect(groups).toHaveCount(2);
    // Newest first.
    const newest = groups.nth(0);
    await expect(newest).toHaveAttribute("data-group", "g-new");
    await expect(newest.getByTestId("acceptance-source")).toHaveText("Payrudder payment page");
    const items = newest.getByTestId("acceptance-item");
    await expect(items).toHaveCount(2);
    await expect(items.nth(0)).toContainText("Distance sales contract · Version 2");
    await expect(items.nth(0)).toContainText("Türkçe (asked for English)");
    await expect(items.nth(1)).toContainText("Pre-information form");
    await expect(newest.getByTestId("acceptance-consents")).toContainText("I ask for the service to start now");
    await expect(newest.getByTestId("acceptance-consents")).toContainText("I know I lose the right of withdrawal");
    await expect(newest.locator('[data-field="ip"]')).toHaveText("Observed by Payrudder");
    // Observed, but blank: the retention ran out.
    await expect(newest.locator('[data-field-value="ip"]')).toHaveAttribute("data-state", "erased");
    await expect(newest.locator('[data-field-value="ip"]')).toContainText("Erased after the retention period");
    await expect(newest.locator('[data-field-value="user_agent"]')).toContainText("Mozilla/5.0");

    // The older one came through the seller's API: everything is its claim, and nothing was given for the address.
    const older = groups.nth(1);
    await expect(older.getByTestId("acceptance-source")).toHaveText("Reported by your API");
    for (const f of ["time", "text", "ip", "user_agent"]) await expect(older.locator(`[data-field="${f}"]`)).toHaveText("Reported by you");
    await expect(older.locator('[data-field-value="ip"]')).toHaveAttribute("data-state", "none");
    await expect(older.getByTestId("acceptance-consents")).toHaveCount(0);

    await expect(page.getByTestId("customer-withdrawal")).toContainText("Mail from the customer");
  });

  test("the page a customer accepted opens in a frame that is sandboxed, from srcdoc, and nothing in it runs", async ({ page }) => {
    await openSheet(page);
    await page.getByTestId("acceptance-group").first().getByTestId("acceptance-view").click();
    const dialog = page.getByTestId("snapshot-dialog");
    const frame = dialog.locator("iframe");
    await expect(frame).toHaveCount(1);
    await expect(frame).toHaveAttribute("sandbox", "");
    await expect(frame).toHaveAttribute("srcdoc", SNAPSHOT_HTML);
    await expect(dialog.getByTestId("snapshot-sha256")).toHaveText("cafe".repeat(16));
    // The page is inside the frame, never in this document, and its script did not run.
    await expect(page.frameLocator('[data-testid="snapshot-dialog"] iframe').getByRole("heading", { name: "Accepted page" })).toBeVisible();
    await expect(dialog.getByRole("heading", { name: "Accepted page" })).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();
  });

  test("a withdrawal that reached the seller by mail is recorded from the console, with its words and when it arrived", async ({ page }) => {
    const { gw } = await openSheet(page);
    await page.getByTestId("withdrawal-record").click();
    await expect(page.getByTestId("withdrawal-record-submit")).toBeDisabled();
    await page.getByTestId("withdrawal-statement").fill("I withdraw from the contract, see my mail");
    await page.getByTestId("withdrawal-received").fill("2026-10-20T09:30");
    await page.getByTestId("withdrawal-record-submit").click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/contract-withdrawals").length).toBe(1);
    const sent = gw.called("POST", "/v1/customers/cus_1/contract-withdrawals")[0]?.body;
    expect(sent).toEqual({ statement: "I withdraw from the contract, see my mail", source: "console", receive_time: await page.evaluate(() => new Date("2026-10-20T09:30").toISOString()) });
    await expect(page.getByTestId("customer-withdrawal").first()).toContainText("I withdraw from the contract, see my mail");
    await expect(page.getByTestId("customer-withdrawal").first()).toContainText("Recorded in the console");
    // No invoice was chosen, so none is sent: the seller refunds by hand.
    expect(sent).not.toHaveProperty("invoice_id");
  });

  test("a withdrawal can name the invoice the customer withdraws from, chosen among their paid invoices", async ({ page }) => {
    const { gw } = await openSheet(page, { invoices: [invoice("inv_a", "cus_1", "4900"), invoice("inv_b", "cus_1", "1500", { number: "INV-B", finalize_time: "2026-09-01T10:00:00Z" }), invoice("inv_x", "cus_2", "999")] });
    await page.getByTestId("withdrawal-record").click();
    const select = page.getByTestId("withdrawal-invoice");
    await expect(select.locator("option")).toHaveCount(3);
    await expect(select.locator("option").nth(1)).toContainText("INV-inv_a");
    await expect(select.locator("option").nth(1)).toContainText("$49.00");
    await select.selectOption("inv_b");
    await page.getByTestId("withdrawal-statement").fill("Cancel my order");
    await page.getByTestId("withdrawal-record-submit").click();
    await expect.poll(() => gw.called("POST", "/v1/customers/cus_1/contract-withdrawals").length).toBe(1);
    expect(gw.called("POST", "/v1/customers/cus_1/contract-withdrawals")[0]?.body).toMatchObject({ statement: "Cancel my order", source: "console", invoice_id: "inv_b" });
  });

  test("the log is read a page at a time and narrowed by kind and by where it was accepted", async ({ page }) => {
    // 25 checkout moments, one version each: the terms and the privacy notice alternate, and the seller's API reported every sixth (all of them terms), newest first.
    const many = Array.from({ length: 25 }, (_, i) =>
      acceptance(`a${String(i).padStart(2, "0")}`, `g${String(i).padStart(2, "0")}`, i % 2 === 0 ? "tos" : "privacy", new Date(Date.UTC(2026, 9, 25 - i, 10)).toISOString(), { source: i % 6 === 0 ? "api" : "hosted" }),
    );
    const { gw } = await openSheet(page, { acceptances: many });
    const groups = page.getByTestId("acceptance-group");
    await expect(groups).toHaveCount(20);
    await expect(page.getByTestId("acceptance-more")).toBeVisible();
    expect(gw.called("GET", "/v1/customers/cus_1/contract-acceptances")[0]?.query.get("page_size")).toBe("20");
    await page.getByTestId("acceptance-more").click();
    await expect(groups).toHaveCount(25);
    await expect(page.getByTestId("acceptance-more")).toHaveCount(0);

    // A filter asks the server again from the first page; the paging starts over.
    await page.getByTestId("acceptance-filter-kind").selectOption("tos");
    await expect(groups).toHaveCount(13);
    await expect(groups.first().getByTestId("acceptance-item")).toContainText("Terms of service");
    const last = gw.called("GET", "/v1/customers/cus_1/contract-acceptances").at(-1);
    expect(last?.query.get("kind")).toBe("tos");
    expect(last?.query.get("page_token")).toBeNull();
    await page.getByTestId("acceptance-filter-source").selectOption("api");
    await expect(groups).toHaveCount(5);
    expect(gw.called("GET", "/v1/customers/cus_1/contract-acceptances").at(-1)?.query.get("source")).toBe("api");
    await page.getByTestId("acceptance-filter-kind").selectOption("privacy");
    await expect(page.getByTestId("acceptances-empty")).toContainText("Nothing matches these filters.");
    // Clearing brings back the pages already read.
    await page.getByTestId("acceptance-filter-clear").click();
    await expect(groups).toHaveCount(25);
  });
});

test.describe("the go-live list", () => {
  const ALL_DONE = { email_verified: true, legal_profile: true, payment_provider: true, plan_published: true, terms_accepted: true };

  async function goLive(page: import("@playwright/test").Page, contracts: Parameters<typeof contractsGateway>[1]) {
    const { gw } = productGateway(new MockGateway(), { apps: [appWithLive("INACTIVE")] });
    liveGateway(gw, { done: ALL_DONE });
    contractsGateway(gw, contracts, LIVE);
    await gw.install(page);
    await signInAsAda(page);
    await page.goto(`/app/apps/${APP}/go-live?env=live`);
    return gw;
  }

  test("is advice when the seller requires nothing: neutral, not counted, never in the way of going live", async ({ page }) => {
    await goLive(page, { versions: [] });
    const row = page.getByTestId("go-live-agreements");
    await expect(row).toHaveAttribute("data-state", "optional");
    await expect(row).toContainText("Optional: needed if you sell to consumers");
    await expect(page.getByTestId("go-live-state")).toContainText("5 of 5 done");
    await expect(page.getByTestId("go-live-activate")).toBeEnabled();
  });

  test("is done once every required agreement is published", async ({ page }) => {
    await goLive(page, { versions: [version("tos", "consumer", 1, { en: "Terms" })], settings: { required_kinds: ["tos"], immediate_start: false, retention_months: 36 } });
    await expect(page.getByTestId("go-live-agreements")).toHaveAttribute("data-state", "ready");
    await expect(page.getByTestId("go-live-agreements")).toContainText("Every agreement you require is published");
  });

  test("warns while a required agreement still carries the starter text, and still lets the owner go live", async ({ page }) => {
    await goLive(page, { versions: [version("tos", "consumer", 1, { en: STARTER_EN })], settings: { required_kinds: ["tos"], immediate_start: false, retention_months: 36 } });
    const row = page.getByTestId("go-live-agreements");
    await expect(row).toHaveAttribute("data-state", "template");
    await expect(row).toContainText("still carries the starter text");
    await expect(row.getByTestId("go-live-go-agreements")).toHaveAttribute("href", new RegExp(`^/app/t/${LIVE}/contracts(\\?|$)`));
    await expect(page.getByTestId("go-live-state")).toContainText("5 of 5 done");
    await expect(page.getByTestId("go-live-activate")).toBeEnabled();
  });
});

test("the page is in a person's sidebar under Contracts", async ({ page }) => {
  const { gw } = productGateway(new MockGateway());
  contractsGateway(gw, { versions: [version("privacy", "consumer", 1, { en: "Privacy" })] });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/contracts`);
  await expect(page.getByRole("heading", { level: 1, name: "Contracts" })).toBeVisible();
  await expect(page.getByTestId("sidebar-nav").getByRole("link", { name: "Contracts" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByTestId("agreement-privacy~consumer").getByTestId("agreement-number")).toHaveText("Version 1");
});
