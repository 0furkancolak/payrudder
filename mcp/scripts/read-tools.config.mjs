// Which operations of api/openapi/v1.yaml become read tools, and how the text in their answers is treated. Everything else about a tool (its name, its arguments, their
// limits, its description) is read from the OpenAPI file by scripts/generate.mjs, so a tool cannot drift from the API; a change here is a decision about what an assistant
// may read.

/**
 * The allow-list: operationId -> overrides. Only GET operations of the tenant API (/v1/*) can be listed. `args` renames an API parameter to the argument the tool
 * takes (kept as the first nine tools named them, so an assistant's saved prompts keep working).
 */
export const READ_TOOLS = {
  listMeters: {},
  listCustomers: {},
  getCustomer: { args: { external_id: "customer_external_id" } },
  getCustomerUsage: { args: { external_id: "customer_external_id" } },
  listPlans: {},
  listSubscriptions: { args: { customer: "customer_external_id" } },
  listInvoices: { args: { customer: "customer_external_id" } },
  getInvoice: { args: { id: "invoice_id" } },
  listPayments: {},
};

/**
 * Fields whose text is written by people (the account's customers, their payers, its staff), not by Payrudder. In an answer, every string under one of these keys is
 * wrapped in <untrusted>...</untrusted>, so a model can tell a customer's note from the API's own data. An object or list under such a key is tagged all the way down.
 */
export const UNTRUSTED = [
  "name", "email", "description", "note", "block_reason", "line1", "line2", "city", "region", "postal_code", "tax_id", "tax_office", "unit_label", "reference",
  "recorded_by", "category", "coupon_code", "values",
];

/**
 * Free-text fields of the answers that are not people's words: identifiers, codes and numbers the API makes or constrains, which the model has to copy exactly into the
 * next call (so they are not tagged). `external_id` and `customer_external_id` are chosen by the account's own app; the tools' arguments bound their length.
 * The generator fails when an allow-listed operation's answer has a free-text field in neither list, so a new field is classified before it reaches a model.
 */
export const STRUCTURAL = [
  "billing_currency", "block_category", "changed_from_id", "code", "country", "currency", "customer_external_id", "customer_id", "customer_type", "external_id", "id",
  "invoice_id", "locale", "meter_code", "next_page_token", "number", "origin_period_id", "payment_method", "pdf_object_key", "period_id", "plan_code", "plan_version_id",
  "provider", "provider_ref", "quantity", "saga_state", "value_property",
];
