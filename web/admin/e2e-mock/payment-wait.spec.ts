import { expect, test, type Page } from "@playwright/test";
import { MockGateway } from "./gateway";
import { ACCOUNT, LIVE, billingGateway, invoice, invoiceLines, productGateway, signInAsAda } from "./product";
import { confirmDialog } from "./ui";

// An invoice whose payment waits (plans 13, 14, 08): a first charge held by the payment gate says why and lets an owner or an administrator release or void it, after a
// question that names the amount, and gives a viewer no button; a payment that needs the customer's 3-D Secure says that the customer was mailed, since when, and when it counts
// as declined.

test.use({ viewport: { width: 1440, height: 900 } });

const HELD = "d0000000-0000-4000-8000-0000000000a1";
const WAITING = "d0000000-0000-4000-8000-0000000000a2";
const held = (extra: Record<string, unknown> = {}) => invoice(HELD, "INV-2026-000009", "open", "12500", "cus_blocked", { saga_state: "PAYMENT_HELD", lines: invoiceLines, payments: [], ...extra });
const waiting = invoice(WAITING, "INV-2026-000010", "open", "4900", "cus_3ds", { saga_state: "PAYMENT_ACTION_REQUIRED", lines: invoiceLines, payments: [] });
const wait = (extra: Record<string, unknown>) => ({ state: "PAYMENT_HELD", since_time: "2026-10-21T08:00:00Z", next_check_time: "2026-10-21T13:00:00Z", hold_reason: "customer_blocked", ...extra });

async function open(page: Page, opts: { role?: string; wait?: Record<string, unknown>; after?: string } = {}) {
  let voided = false;
  const role = opts.role ?? "owner";
  const { gw } = productGateway(new MockGateway(), { role, memberships: [{ account_id: ACCOUNT, account_name: "Ada Labs", role }] });
  billingGateway(gw);
  gw.on((c) => {
    if (c.method === "GET" && c.path === "/v1/invoices") return { body: { invoices: [voided ? held({ status: "void", saga_state: "VOIDED" }) : held(), waiting], next_page_token: "" } };
    if (c.method === "GET" && c.path === `/v1/invoices/${HELD}`) return { body: voided ? held({ status: "void", saga_state: "VOIDED" }) : held() };
    if (c.method === "GET" && c.path === `/v1/invoices/${WAITING}`) return { body: waiting };
    if (c.method === "GET" && c.path === `/v1/invoices/${HELD}/payment-wait`) return { body: wait(opts.wait ?? {}) };
    if (c.method === "GET" && c.path === `/v1/invoices/${WAITING}/payment-wait`) {
      return { body: { state: "PAYMENT_ACTION_REQUIRED", since_time: "2026-10-20T09:00:00Z", next_check_time: "2026-10-21T15:00:00Z", window_ends_time: "2026-11-03T08:59:00Z" } };
    }
    if (c.method === "POST" && c.path === `/v1/invoices/${HELD}/release`) return { body: { invoice_id: HELD, state: opts.after ?? "PAYMENT_HELD" } };
    if (c.method === "POST" && c.path === `/v1/invoices/${HELD}/void`) {
      voided = true;
      return { body: { invoice_id: HELD, state: "VOIDED" } };
    }
    return undefined;
  });
  await gw.install(page);
  await signInAsAda(page);
  await page.goto(`/app/t/${LIVE}/invoices`);
  return gw;
}

const sheet = (page: Page) => page.getByTestId("detail-sheet");

test("a held first charge says why, since when and when it is looked at again, and an owner can release it after a question that names the amount", async ({ page }) => {
  const gw = await open(page);
  await page.getByTestId("invoice-INV-2026-000009").click();
  const card = sheet(page).getByTestId("payment-wait");
  await expect(card).toContainText("First charge on hold");
  await expect(card.getByTestId("hold-reason")).toHaveAttribute("data-reason", "customer_blocked");
  await expect(card.getByTestId("hold-reason")).toContainText("blocked for fraud or a chargeback");
  await expect(card).toContainText("On hold since");
  await expect(card).toContainText("Next check");
  expect(gw.called("GET", `/v1/invoices/${HELD}/payment-wait`)).toHaveLength(1);

  await card.getByTestId("held-release").click();
  const dialog = page.getByTestId("confirm-dialog");
  await expect(dialog).toContainText("Release the charge of INV-2026-000009?");
  await expect(dialog).toContainText("$125.00");
  await expect(dialog).toContainText("cus_blocked");
  await confirmDialog(page, "no");
  expect(gw.called("POST", /\/v1\/invoices\/.+\/(release|void)$/)).toHaveLength(0);

  await card.getByTestId("held-release").click();
  await confirmDialog(page, "yes");
  await expect.poll(() => gw.called("POST", `/v1/invoices/${HELD}/release`).length).toBe(1);
  await expect(page.getByText("Still on hold: the customer is still blocked")).toBeVisible();
  expect(gw.called("POST", `/v1/invoices/${HELD}/void`)).toHaveLength(0);
});

test("an owner can void a held invoice after a question that names the amount, and the invoice reads void afterwards", async ({ page }) => {
  const gw = await open(page);
  await page.getByTestId("invoice-INV-2026-000009").click();
  await sheet(page).getByTestId("held-void").click();
  const dialog = page.getByTestId("confirm-dialog");
  await expect(dialog).toContainText("Void INV-2026-000009?");
  await expect(dialog).toContainText("$125.00 will not be charged");
  await confirmDialog(page, "no");
  expect(gw.called("POST", `/v1/invoices/${HELD}/void`)).toHaveLength(0);

  await sheet(page).getByTestId("held-void").click();
  await confirmDialog(page, "yes");
  await expect.poll(() => gw.called("POST", `/v1/invoices/${HELD}/void`).length).toBe(1);
  await expect(sheet(page).getByTestId("payment-wait")).toHaveCount(0);
  await expect(sheet(page)).toContainText("Void");
});

test("a released charge is announced as sent", async ({ page }) => {
  const gw = await open(page, { wait: { hold_reason: "none" }, after: "PAYMENT_REQUESTED" });
  await page.getByTestId("invoice-INV-2026-000009").click();
  await expect(sheet(page).getByTestId("hold-reason")).toContainText("Nothing holds this invoice now");
  await sheet(page).getByTestId("held-release").click();
  await confirmDialog(page, "yes");
  await expect.poll(() => gw.called("POST", `/v1/invoices/${HELD}/release`).length).toBe(1);
  await expect(page.getByText("Released: the charge of $125.00 was sent")).toBeVisible();
});

test("a viewer reads why it is held and is given no button", async ({ page }) => {
  await open(page, { role: "viewer" });
  await page.getByTestId("invoice-INV-2026-000009").click();
  const card = sheet(page).getByTestId("payment-wait");
  await expect(card.getByTestId("hold-reason")).toBeVisible();
  await expect(card.getByTestId("held-release")).toHaveCount(0);
  await expect(card.getByTestId("held-void")).toHaveCount(0);
  await expect(card.getByTestId("held-viewer")).toContainText("Only an owner or an administrator");
});

test("while the operator has the environment on hold nobody is offered a button, and the card says where to write", async ({ page }) => {
  await open(page, { wait: { hold_reason: "tenant_on_hold" } });
  await page.getByTestId("invoice-INV-2026-000009").click();
  const card = sheet(page).getByTestId("payment-wait");
  await expect(card.getByTestId("hold-reason")).toContainText("put this environment on hold");
  await expect(card.getByTestId("held-release")).toHaveCount(0);
  await expect(card.getByTestId("held-void")).toHaveCount(0);
});

test("a payment that needs the customer says they were mailed, since when, and when it counts as declined; it offers no button", async ({ page }) => {
  const gw = await open(page);
  await page.getByTestId("invoice-INV-2026-000010").click();
  const card = sheet(page).getByTestId("payment-wait");
  await expect(card).toContainText("Waiting for the customer to confirm the payment");
  await expect(card).toContainText("Customer mailed");
  await expect(card).toContainText("Counts as declined on");
  await expect(card).toContainText("Nov 3, 2026");
  await expect(card.getByRole("button")).toHaveCount(0);
  expect(gw.called("GET", `/v1/invoices/${WAITING}/payment-wait`)).toHaveLength(1);
});

test("an invoice whose payment is not waiting asks nothing", async ({ page }) => {
  const gw = await open(page);
  await page.goto(`/app/t/${LIVE}/invoices?sheet=invoice:d0000000-0000-4000-8000-000000000001`);
  await expect(sheet(page)).toBeVisible();
  await expect(sheet(page).getByTestId("payment-wait")).toHaveCount(0);
  expect(gw.calls.filter((c) => c.path.endsWith("/payment-wait"))).toHaveLength(0);
});
