// Which operations of api/openapi/v1.yaml become read tools, and how the text in their answers is treated. Everything else about a tool (its name, its arguments, their
// limits, its description) is read from the OpenAPI file by scripts/generate.mjs, so a tool cannot drift from the API; a change here is a decision about what an assistant
// may read.

/**
 * The allow-list: operationId -> overrides. Only GET operations of the tenant API (/v1/*) can be listed. `args` renames an API parameter to the argument the tool
 * takes (kept as the first nine tools named them, so an assistant's saved prompts keep working).
 */
export const READ_TOOLS = {
  // The first nine (2026-11-02, the names an assistant's saved prompts use).
  listMeters: {},
  listCustomers: {},
  getCustomer: { args: { external_id: "customer_external_id" } },
  getCustomerUsage: { args: { external_id: "customer_external_id" } },
  listPlans: {},
  listSubscriptions: { args: { customer: "customer_external_id" } },
  listInvoices: { args: { customer: "customer_external_id" } },
  getInvoice: { args: { id: "invoice_id" } },
  listPayments: {},
  // Added with the prompts (default, not agreed): what "summarise this customer's billing", "why did this invoice come out like this" and "monthly revenue review"
  // need, and what a billing assistant asks next. Each is a plain GET of the tenant API with no secret, token, link or raw provider payload in its answer.
  getMeter: {}, // one meter's aggregation and unit, to read a line of an invoice
  getSubscription: {}, // one subscription: state, plan, coupon, a scheduled change
  getUpcomingInvoice: { args: { external_id: "customer_external_id" } }, // the lines the next invoice would have, with the credit and the tax
  getPlanVersion: {}, // the prices behind an invoice line (per unit, tiers, packages, included quantity)
  listEntitlements: { args: { external_id: "customer_external_id" } }, // usage against the limits: "is this customer near or over a limit"
  getEntitlement: { args: { external_id: "customer_external_id" } }, // its description explains the items of the list
  getCreditBalance: { args: { external_id: "customer_external_id" } }, // what a customer can spend now, in lots
  listCreditLedger: { args: { external_id: "customer_external_id" } }, // why the balance is what it is
  listCreditNotes: {}, // what was given back on an invoice
  getCreditNote: {},
  listCustomerPayments: { args: { external_id: "customer_external_id" } }, // a customer's invoices with their lines and the payments on them, in one call
  listCoupons: {}, // a discount on a line of an invoice
  getTaxRates: {}, // the tax on an invoice
  getRevenueReport: {}, // aggregates only: the monthly revenue review
  getSubscriptionReport: {},
  getDunningReport: {},
};

/**
 * Reads of the tenant API that are NOT offered, and why (default, not agreed; the owner may overturn it). A test checks that none of them is allow-listed and that each
 * is still a GET of the OpenAPI file, so a renamed operation cannot silently drop out of this list. A link or a secret in an answer is the reason most often: the model
 * would hold a bearer link, and a prompt-injected model could pass it on.
 */
export const NOT_OFFERED = {
  getInvoicePdf: "a presigned link that opens the invoice for anyone who holds it",
  downloadAsset: "a presigned link",
  getEInvoiceFile: "a presigned link to the legal e-document",
  getInboxItemFile: "the bytes of a received mail attachment",
  getContractSnapshot: "the page a customer accepted, with the address and browser they used",
  listContractAcceptances: "a customer's address and browser (personal data) for every acceptance",
  listWebhookEndpoints: "an endpoint's address can carry a token in its path or query, and the model has no use for it",
  listWebhookDeliveries: "the answer body the customer's own server gave, which anyone can write",
  listAuditEvents: "staff e-mail addresses and network addresses, and free text of the detail",
  exportAuditEvents: "same as listAuditEvents, as a file",
  exportAccountingCsv: "a file, not a JSON answer",
  exportAccountingCouponsCsv: "a file, not a JSON answer",
  exportAffiliateJournal: "a file, not a JSON answer",
  listPromotionCodes: "the codes customers redeem, which are the discount itself",
  getExpenseInbox: "the tenant's inbox address, which anyone could send mail to",
  listInboxItems: "attachments and senders of received mail",
  getUploadFile: "an upload session",
  sandboxScenarios: "the sandbox simulator, not the account's data",
};

/**
 * Fields whose text is written by people (the account's customers, their payers, its staff), not by Payrudder. In an answer, every string under one of these keys is
 * wrapped in <untrusted>...</untrusted>, so a model can tell a customer's note from the API's own data. An object or list under such a key is tagged all the way down.
 */
export const UNTRUSTED = [
  "name", "email", "description", "note", "block_reason", "line1", "line2", "city", "region", "postal_code", "tax_id", "tax_office", "unit_label", "reference",
  "recorded_by", "category", "coupon_code", "values", "phone", "tax_id_warning",
  // A credit note's or a ledger entry's reason is written by staff, a refund error and a dunning error are the payment provider's words, and an idempotency key is the app's
  // own string: none of them is an instruction, and none has to be copied into a call.
  "reason", "refund_error", "last_error", "idempotency_key",
];

/**
 * Of the UNTRUSTED keys, the ones that identify or reach a person: their contact data, tax identity and address. They are replaced by a marker in every answer unless the
 * person who starts the server sets PAYRUDDER_MCP_ALLOW_PERSONAL_DATA=1 (src/privacy.ts). `name` is not listed: a plan's or a meter's name is the seller's own, so src/privacy.ts
 * redacts it only beside an `email` or an `external_id` (a customer), and everything under an `address` except its country. Staff's free text (notes, reasons, block_reason)
 * is not in this list on purpose: it is the seller's own record, and redacting it would stop an assistant explaining a block or a credit note.
 */
export const PERSONAL = ["email", "phone", "tax_id", "tax_office", "line1", "line2", "city", "region", "postal_code"];

/**
 * Free-text fields of the answers that are not people's words: identifiers, codes and numbers the API makes or constrains, which the model has to copy exactly into the
 * next call (so they are not tagged). `external_id` and `customer_external_id` are chosen by the account's own app; the tools' arguments bound their length.
 * The generator fails when an allow-listed operation's answer has a free-text field in neither list, so a new field is classified before it reaches a model.
 */
export const STRUCTURAL = [
  "billing_currency", "block_category", "changed_from_id", "code", "country", "currency", "customer_external_id", "customer_id", "customer_type", "external_id", "id",
  "invoice_id", "locale", "meter_code", "next_page_token", "number", "origin_period_id", "payment_method", "pdf_object_key", "period_id", "plan_code", "plan_version_id",
  "provider", "provider_ref", "quantity", "saga_state", "value_property",
  // Decimals of a plan and of usage (sent as text), the window an entitlement counts in, an invoice's number and the group key or month of a report: made by the API.
  "included", "limit", "package_size", "unit_price", "up_to", "used", "window_id", "invoice_number", "period", "month", "state",
];
