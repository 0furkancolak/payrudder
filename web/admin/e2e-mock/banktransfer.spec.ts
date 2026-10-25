import { expect, test } from "@playwright/test";
import { baseTenant, MockGateway, openTenantPage, TENANT } from "./gateway";

// Phase 29: the bank transfer account form, the pending transfer on the invoice page, and recording payments by hand.

test.describe("bank transfer account", () => {
  const quiet = (c: { path: string; method: string }) => {
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-accounts` && c.method === "GET") return { body: { accounts: [] } };
    if (c.path === `/admin/v1/tenants/${TENANT}/provider-subscriptions`) return { body: { subscriptions: [] } };
    if (c.path === "/v1/routing-rules" && c.method === "GET") return { body: { rules: [] } };
    return undefined;
  };
  const put = `/admin/v1/tenants/${TENANT}/provider-accounts/banktransfer`;

  test("the structured form posts the normalised settings", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) => (c.method === "PUT" && c.path === put ? { body: { provider: "banktransfer" } } : undefined));
    await gw.install(page);
    await openTenantPage(page, "/providers");
    const form = page.getByRole("form", { name: "Add or replace an account" });
    await form.getByLabel("Provider").selectOption("banktransfer");
    await expect(form.getByLabel("Settings (JSON)")).toHaveCount(0);
    await expect(form.getByText("{invoice_number} and {invoice_id} are available")).toBeVisible();
    await expect(form.getByText("0 or empty uses the deployment default")).toBeVisible();

    await form.getByLabel("Bank name").fill("Akbank");
    await form.getByLabel("Account holder").fill("Acme A.S.");
    await form.getByLabel("IBAN").fill("tr33 0006 1005 1978 6457 8413 26");
    await form.getByLabel("BIC (optional)").fill("akbktris");
    await form.getByLabel("Payment reference (optional)").fill("ACME {invoice_number}");
    await form.getByLabel("TRY", { exact: true }).click();
    await form.getByLabel("EUR", { exact: true }).click();
    await form.getByLabel("Payment window (days)").fill("21");
    await form.getByRole("button", { name: "Save account" }).click();

    await expect.poll(() => gw.called("PUT", put).length).toBe(1);
    const call = gw.called("PUT", put)[0];
    expect(call?.query.get("environment")).toBe("live");
    expect(call?.query.get("default")).toBe("true");
    expect(call?.body).toEqual({
      bank_name: "Akbank",
      account_holder: "Acme A.S.",
      iban: "TR330006100519786457841326",
      bic: "AKBKTRIS",
      reference_template: "ACME {invoice_number}",
      currencies: ["TRY", "EUR"],
      payment_window_days: 21,
    });
    await expect(page.getByRole("status").filter({ hasText: "Saved." })).toBeVisible();
  });

  test("a wrong IBAN is caught before anything is sent, and sandbox says it never reaches the provider", async ({ page }) => {
    const gw = baseTenant(new MockGateway()).on(quiet);
    await gw.install(page);
    await openTenantPage(page, "/providers");
    const form = page.getByRole("form", { name: "Add or replace an account" });
    await form.getByLabel("Provider").selectOption("banktransfer");
    await form.getByLabel("Environment").selectOption("sandbox");
    await expect(form.getByRole("note")).toContainText("Sandbox charges never reach this provider");
    await form.getByLabel("Bank name").fill("Akbank");
    await form.getByLabel("Account holder").fill("Acme A.S.");
    await form.getByLabel("IBAN").fill("TR33 0006 1005 1978 6457 8413 62");
    await form.getByRole("button", { name: "Save account" }).click();
    await expect(form.getByRole("alert")).toContainText("check digits do not match");
    expect(gw.called("PUT", put)).toHaveLength(0);
  });

  test("a refusal from the server is shown by its key", async ({ page }) => {
    const gw = baseTenant(new MockGateway())
      .on(quiet)
      .on((c) =>
        c.method === "PUT" && c.path === put
          ? { status: 400, body: { error: { code: "INVALID_ARGUMENT", key: "payments.transfer.unavailable", message: "payments service unavailable (raw)" } } }
          : undefined,
      );
    await gw.install(page);
    await openTenantPage(page, "/providers");
    const form = page.getByRole("form", { name: "Add or replace an account" });
    await form.getByLabel("Provider").selectOption("banktransfer");
    await form.getByLabel("Bank name").fill("Akbank");
    await form.getByLabel("Account holder").fill("Acme A.S.");
    await form.getByLabel("IBAN").fill("DE89 3704 0044 0532 0130 00");
    await form.getByRole("button", { name: "Save account" }).click();
    await expect(page.getByRole("alert")).toContainText("payments service is not available right now");
  });
});

test.describe("invoice money", () => {
  const base = `/admin/v1/tenants/${TENANT}/invoices/inv1`;
  const invoice = (extra: Record<string, unknown> = {}) => ({
    id: "inv1",
    number: "INV-2026-000007",
    customer_external_id: "cus_1",
    period_id: "2026-10",
    kind: "REGULAR",
    currency: "USD",
    total_minor: "10000",
    status: "open",
    saga_state: "PAYMENT_PENDING",
    lines: [],
    ...extra,
  });
  const transfer = (extra: Record<string, unknown> = {}) => ({
    invoice_id: "inv1",
    state: "pending",
    reference: "INV-2026-000007",
    instructions: { bank_name: "Akbank", account_holder: "Acme A.S.", iban: "TR330006100519786457841326", bic: "AKBKTRIS", reference: "INV-2026-000007" },
    currency: "USD",
    total_minor: 10000,
    received_minor: 0,
    outstanding_minor: 10000,
    pending_since: "2026-10-18T10:00:00Z",
    expires_at: "2026-11-01T10:00:00Z",
    receipts: [],
    ...extra,
  });
  /** An invoice page whose answers the test can change: `state.invoice` and `state.transfer` (null: the transfer service is unavailable). */
  function invoicePage(state: { invoice: Record<string, unknown>; transfer: Record<string, unknown> | null }) {
    return baseTenant(new MockGateway()).on((c) => {
      if (c.method !== "GET") return undefined;
      if (c.path === base) return { body: state.invoice };
      if (c.path === `${base}/saga`) return { body: { state: String(state.invoice.saga_state), events: [] } };
      if (c.path === `${base}/transfer`) return state.transfer ? { body: state.transfer } : { status: 503, body: { error: { code: "UNAVAILABLE", key: "payments.transfer.unavailable", message: "down" } } };
      return undefined;
    });
  }

  test("a pending transfer shows the reference, the bank details and what is outstanding", async ({ page }) => {
    const gw = invoicePage({
      invoice: invoice(),
      transfer: transfer({
        received_minor: 2500,
        outstanding_minor: 7500,
        receipts: [{ id: 1, amount_minor: 2500, currency: "USD", received_at: "2026-10-19T09:00:00Z", bank_reference: "BANK-1", note: "first half", recorded_by: "console", recorded_at: "2026-10-19T09:05:00Z" }],
      }),
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    const card = page.getByTestId("transfer-card");
    await expect(card.getByTestId("transfer-state")).toHaveText("waiting for the money");
    await expect(card.getByTestId("transfer-reference")).toHaveText("INV-2026-000007");
    await expect(card.getByTestId("transfer-iban")).toHaveText("TR330006100519786457841326");
    await expect(card).toContainText("Akbank");
    await expect(card).toContainText("Acme A.S.");
    await expect(card.getByTestId("transfer-received")).toContainText("25.00");
    await expect(card.getByTestId("transfer-outstanding")).toContainText("75.00");
    await expect(card.getByRole("row", { name: /BANK-1/ })).toContainText("first half");
    // The saga state has a readable label.
    await expect(page.getByText("waiting for transfer").first()).toBeVisible();
  });

  test("without transfer information (503) the invoice page still works", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice({ saga_state: "PAYMENT_DECLINED" }), transfer: null });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByRole("heading", { name: "INV-2026-000007" })).toBeVisible();
    await expect(page.getByTestId("transfer-card")).toHaveCount(0);
    // The invoice is payable, so it can be recorded by hand.
    await expect(page.getByRole("button", { name: "Record payment" })).toBeVisible();
  });

  test("an expired transfer is shown as such", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice({ saga_state: "PAYMENT_DECLINED" }), transfer: transfer({ state: "expired", closed_reason: "transfer_expired" }) });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByTestId("transfer-state")).toHaveText("expired");
    await expect(page.getByTestId("transfer-card")).toContainText("transfer_expired");
    await expect(page.getByRole("button", { name: "Mark expired" })).toHaveCount(0);
  });

  test("a partial transfer is confirmed with the bank reference and the invoice is partly paid", async ({ page }) => {
    const state = { invoice: invoice(), transfer: transfer() as Record<string, unknown> | null };
    const gw = invoicePage(state).on((c) => {
      if (c.method === "POST" && c.path === `${base}/transfer/receipts`) {
        state.transfer = transfer({
          received_minor: 4000,
          outstanding_minor: 6000,
          receipts: [{ id: 1, amount_minor: 4000, currency: "USD", received_at: String(c.body?.received_at), bank_reference: String(c.body?.bank_reference), recorded_by: "console" }],
        });
        return { status: 201, body: { receipt: (state.transfer.receipts as unknown[])[0], duplicate: false, paid: false, saga_notified: true, transfer: state.transfer } };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Confirm bank transfer" });
    await expect(dialog.getByLabel("Amount (USD)")).toHaveValue("100.00");
    await expect(dialog.getByLabel("Method")).toHaveCount(0);

    // The bank reference is required here.
    await dialog.getByLabel("Amount (USD)").fill("40.00");
    await dialog.getByRole("button", { name: "Confirm transfer" }).click();
    await expect(dialog.getByRole("alert")).toContainText("bank reference is required");
    expect(gw.called("POST", `${base}/transfer/receipts`)).toHaveLength(0);

    await dialog.getByLabel("Bank reference").fill("BANK-77");
    await dialog.getByLabel("Note").fill("half of it");
    await dialog.getByRole("button", { name: "Confirm transfer" }).click();

    await expect.poll(() => gw.called("POST", `${base}/transfer/receipts`).length).toBe(1);
    const call = gw.called("POST", `${base}/transfer/receipts`)[0];
    expect(call?.headers["x-quotify-actor"]).toBe("console");
    expect(call?.body).toMatchObject({ amount_minor: 4000, currency: "USD", bank_reference: "BANK-77", note: "half of it" });
    expect(typeof call?.body?.amount_minor).toBe("number");
    expect(Date.parse(String(call?.body?.received_at))).toBeLessThanOrEqual(Date.now());
    await expect(page.getByText("Partly paid: $40.00 received, $60.00 still outstanding.")).toBeVisible();
    await expect(dialog).toHaveCount(0);
    // The page refetched the transfer.
    await expect(page.getByTestId("transfer-outstanding")).toContainText("60.00");
  });

  test("an overpayment is refused with the translated message", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice(), transfer: transfer() }).on((c) =>
      c.method === "POST" && c.path === `${base}/transfer/receipts`
        ? { status: 409, body: { error: { code: "ALREADY_EXISTS", key: "payments.transfer.overpayment", message: "raw: amount exceeds outstanding 10000" } } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Confirm bank transfer" });
    await dialog.getByLabel("Amount (USD)").fill("150");
    await dialog.getByLabel("Bank reference").fill("BANK-1");
    await dialog.getByRole("button", { name: "Confirm transfer" }).click();
    await expect(dialog.getByRole("alert")).toContainText("Record only the outstanding amount and handle the rest outside Payrudder");
    await expect(dialog.getByRole("alert")).not.toContainText("raw:");
  });

  test("a payment received outside Payrudder is recorded with a string amount and the actor header", async ({ page }) => {
    const state = { invoice: invoice({ saga_state: "PAYMENT_DECLINED" }), transfer: transfer({ state: "none", receipts: [] }) as Record<string, unknown> | null };
    const gw = invoicePage(state).on((c) => {
      if (c.method === "POST" && c.path === "/admin/v1/invoices/inv1/payments") {
        const payment = { id: "pay1", invoice_id: "inv1", method: c.body?.method, amount_minor: c.body?.amount_minor, currency: "USD", received_at: c.body?.received_at, reference: c.body?.reference, note: c.body?.note, recorded_by: "console", recorded_at: "2026-10-20T10:00:00Z" };
        state.invoice = invoice({ saga_state: "PAYMENT_DECLINED", payments: [payment] });
        return { status: 201, body: { payment, duplicate: false, paid_minor: "2550", outstanding_minor: "7450", settled: false } };
      }
      return undefined;
    });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Record payment" });
    await expect(dialog.getByLabel("Amount (USD)")).toHaveValue("100.00");
    await dialog.getByLabel("Method").selectOption("cash");
    await dialog.getByLabel("Amount (USD)").fill("25.50");
    await dialog.getByLabel("Reference").fill("RCPT-9");
    await dialog.getByLabel("Note").fill("paid at the desk");
    await dialog.getByRole("button", { name: "Record payment" }).click();

    await expect.poll(() => gw.called("POST", "/admin/v1/invoices/inv1/payments").length).toBe(1);
    const call = gw.called("POST", "/admin/v1/invoices/inv1/payments")[0];
    expect(call?.headers["x-quotify-actor"]).toBe("console");
    expect(call?.body).toMatchObject({ tenant_id: TENANT, method: "cash", amount_minor: "2550", reference: "RCPT-9", note: "paid at the desk" });
    expect(typeof call?.body?.amount_minor).toBe("string");
    await expect(page.getByText("Partly paid: $25.50 received, $74.50 still outstanding.")).toBeVisible();
    // The recorded payment is listed.
    const list = page.getByRole("region", { name: "Payments recorded by hand" });
    await expect(list.getByTestId("manual-payment")).toContainText("RCPT-9");
    await expect(list.getByTestId("manual-payment")).toContainText("Cash");
    await expect(list.getByTestId("manual-payment")).toContainText("console");
  });

  test("a late transfer for an uncollectible invoice is recorded on the invoice, which settles it", async ({ page }) => {
    const state = { invoice: invoice({ status: "uncollectible", saga_state: "COMPLETED" }), transfer: transfer({ state: "expired", closed_reason: "transfer_expired" }) as Record<string, unknown> | null };
    const gw = invoicePage(state).on((c) =>
      c.method === "POST" && c.path === "/admin/v1/invoices/inv1/payments"
        ? { status: 201, body: { payment: { id: "pay1", invoice_id: "inv1", method: c.body?.method, amount_minor: c.body?.amount_minor }, duplicate: false, paid_minor: "10000", outstanding_minor: "0", settled: true } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Record payment" }); // not "Confirm bank transfer": the saga no longer waits for it
    await dialog.getByLabel("Method").selectOption("bank_transfer");
    await dialog.getByRole("button", { name: "Record payment" }).click();
    await expect.poll(() => gw.called("POST", "/admin/v1/invoices/inv1/payments").length).toBe(1);
    expect(gw.called("POST", `${base}/transfer/receipts`)).toHaveLength(0);
  });

  test("an offline overpayment is refused with the translated message", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice({ saga_state: "PAYMENT_DECLINED" }), transfer: transfer({ state: "none" }) }).on((c) =>
      c.method === "POST" && c.path === "/admin/v1/invoices/inv1/payments"
        ? { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "billing.payment.overpayment", message: "raw overpayment" } } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Record payment" });
    await dialog.getByLabel("Amount (USD)").fill("500");
    await dialog.getByRole("button", { name: "Record payment" }).click();
    await expect(dialog.getByRole("alert")).toContainText("That is more than is outstanding. Record only the outstanding amount.");
  });

  test("a future received date is refused before sending", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice({ saga_state: "PAYMENT_DECLINED" }), transfer: transfer({ state: "none" }) });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByRole("button", { name: "Record payment" }).click();
    const dialog = page.getByRole("dialog", { name: "Record payment" });
    await dialog.getByLabel("Received").fill("2999-01-01T10:00");
    await dialog.getByRole("button", { name: "Record payment" }).click();
    await expect(dialog.getByRole("alert")).toContainText("not in the future");
    expect(gw.called("POST", "/admin/v1/invoices/inv1/payments")).toHaveLength(0);
  });

  test("a pending transfer is rejected after a confirmation that mentions dunning", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice(), transfer: transfer() }).on((c) =>
      c.method === "POST" && c.path === `${base}/transfer/reject` ? { body: transfer({ state: "rejected", closed_reason: "rejected" }) } : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByTestId("transfer-card").getByRole("button", { name: "Reject" }).click();
    const dialog = page.getByRole("dialog", { name: "Reject the bank transfer" });
    await expect(dialog).toContainText("dunning schedule applies at once");
    await dialog.getByLabel("Reason (optional)").fill("customer's bank returned it");
    await dialog.getByRole("button", { name: "Reject" }).click();
    await expect.poll(() => gw.called("POST", `${base}/transfer/reject`).length).toBe(1);
    const call = gw.called("POST", `${base}/transfer/reject`)[0];
    expect(call?.headers["x-quotify-actor"]).toBe("console");
    expect(call?.body).toEqual({ outcome: "rejected", reason: "customer's bank returned it" });
  });

  test("a pending transfer is marked expired", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice(), transfer: transfer() }).on((c) =>
      c.method === "POST" && c.path === `${base}/transfer/reject` ? { body: transfer({ state: "expired" }) } : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByTestId("transfer-card").getByRole("button", { name: "Mark expired" }).click();
    const dialog = page.getByRole("dialog", { name: "Mark the bank transfer expired" });
    await dialog.getByRole("button", { name: "Mark expired" }).click();
    await expect.poll(() => gw.called("POST", `${base}/transfer/reject`).length).toBe(1);
    expect(gw.called("POST", `${base}/transfer/reject`)[0]?.body).toEqual({ outcome: "expired" });
  });

  test("nothing to reject is explained by its key", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice(), transfer: transfer() }).on((c) =>
      c.method === "POST" && c.path === `${base}/transfer/reject`
        ? { status: 409, body: { error: { code: "FAILED_PRECONDITION", key: "payments.transfer.nothing_pending", message: "raw" } } }
        : undefined,
    );
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await page.getByTestId("transfer-card").getByRole("button", { name: "Reject" }).click();
    const dialog = page.getByRole("dialog", { name: "Reject the bank transfer" });
    await dialog.getByRole("button", { name: "Reject" }).click();
    await expect(dialog.getByRole("alert")).toHaveText("There is no pending transfer to reject.");
  });

  test("a paid invoice offers no way to record a payment", async ({ page }) => {
    const gw = invoicePage({ invoice: invoice({ status: "paid", saga_state: "COMPLETED" }), transfer: null });
    await gw.install(page);
    await openTenantPage(page, "/invoices/inv1");
    await expect(page.getByRole("heading", { name: "INV-2026-000007" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Record payment" })).toHaveCount(0);
    expect(gw.called("GET", `${base}/transfer`)).toHaveLength(0);
  });
});
