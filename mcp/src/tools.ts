import { z } from "zod";
import type { Api } from "./api.js";
import { seg } from "./api.js";

/**
 * The tools the server offers. Every one is a thin call of a tenant API route: what a key may do is decided by the key and the API, not here. Reads are always on;
 * a tool that writes (`write: true`) is offered only when the server is started with writes enabled, and says so in its description.
 */
export interface ToolDef {
  name: string;
  title: string;
  description: string;
  input: z.ZodRawShape;
  write?: boolean;
  run(args: Record<string, unknown>, api: Api): Promise<unknown>;
}

const period = z.string().regex(/^[0-9]{4}-(0[1-9]|1[0-2])$/, "a month, like 2026-11");
const pageSize = (max: number) => z.number().int().min(1).max(max).optional().describe(`How many to return (1 to ${max}).`);
const pageToken = z.string().optional().describe("The next_page_token of the previous answer, to read the next page.");
const customer = z.string().min(1).max(200);

export const TOOLS: ToolDef[] = [
  {
    name: "list_meters",
    title: "List meters",
    description: "The meters of the account: what is counted (code, how it adds up, unit).",
    input: {},
    run: (_a, api) => api.get("/v1/meters"),
  },
  {
    name: "list_customers",
    title: "List customers",
    description: "Customers ordered by external id, a page at a time. Use next_page_token for the next page.",
    input: { page_size: pageSize(500), page_token: pageToken },
    run: (a, api) => api.get("/v1/customers", { page_size: a.page_size as number | undefined, page_token: a.page_token as string | undefined }),
  },
  {
    name: "get_customer",
    title: "Get a customer",
    description: "One customer by its external id: contact, country, billing currency, tax data, language, and whether it is blocked.",
    input: { customer_external_id: customer },
    run: (a, api) => api.get(`/v1/customers/${seg(String(a.customer_external_id))}`),
  },
  {
    name: "get_customer_usage",
    title: "Get a customer's usage",
    description: "The usage of a customer in a month, per meter. Without a period, the current month.",
    input: { customer_external_id: customer, period: period.optional() },
    run: (a, api) => api.get(`/v1/customers/${seg(String(a.customer_external_id))}/usage`, { period: a.period as string | undefined }),
  },
  {
    name: "list_plans",
    title: "List plans",
    description: "The plans (packages) that can be sold.",
    input: {},
    run: (_a, api) => api.get("/v1/plans"),
  },
  {
    name: "list_subscriptions",
    title: "List subscriptions",
    description: "Subscriptions, newest first; narrow to one customer with its external id.",
    input: { customer_external_id: customer.optional(), page_size: pageSize(200), page_token: pageToken },
    run: (a, api) => api.get("/v1/subscriptions", { customer: a.customer_external_id as string | undefined, page_size: a.page_size as number | undefined, page_token: a.page_token as string | undefined }),
  },
  {
    name: "list_invoices",
    title: "List invoices",
    description: "Invoices, filtered by customer, billing month or status.",
    input: {
      customer_external_id: customer.optional(),
      period: period.optional(),
      status: z.enum(["draft", "open", "paid", "uncollectible", "void"]).optional(),
      page_size: pageSize(500),
      page_token: pageToken,
    },
    run: (a, api) =>
      api.get("/v1/invoices", {
        customer: a.customer_external_id as string | undefined,
        period: a.period as string | undefined,
        status: a.status as string | undefined,
        page_size: a.page_size as number | undefined,
        page_token: a.page_token as string | undefined,
      }),
  },
  {
    name: "get_invoice",
    title: "Get an invoice",
    description: "One invoice by id, with its lines, totals and state.",
    input: { invoice_id: z.string().min(1).max(100) },
    run: (a, api) => api.get(`/v1/invoices/${seg(String(a.invoice_id))}`),
  },
  {
    name: "list_payments",
    title: "List payments",
    description: "The newest payments (charges and refunds) with their status.",
    input: { limit: z.number().int().min(1).max(200).optional() },
    run: (a, api) => api.get("/v1/payments", { limit: a.limit as number | undefined }),
  },
  {
    name: "create_customer",
    title: "Create a customer",
    description: "WRITES. Creates a customer (external id, name and e-mail are required). Fails if the external id exists.",
    write: true,
    input: {
      external_id: z.string().min(1).max(200),
      name: z.string().min(1).max(200),
      email: z.string().email().max(254),
      country: z.string().length(2).optional().describe("ISO 3166-1 alpha-2, like TR."),
      billing_currency: z.string().length(3).optional().describe("ISO 4217, like EUR."),
    },
    run: (a, api) => api.post("/v1/customers", a),
  },
  {
    name: "ingest_usage_events",
    title: "Send usage events",
    description:
      "WRITES. Records usage events (1 to 100 per call). In live, each event needs an idempotency_key so a retry never counts twice; event_time is an RFC 3339 instant not older than 35 days. Quantities are exact decimals, sent as strings.",
    write: true,
    input: {
      events: z
        .array(
          z.object({
            customer_external_id: customer,
            meter_code: z.string().min(1).max(100),
            quantity: z.string().regex(/^\d+(\.\d+)?$/, "a non-negative decimal, as text"),
            event_time: z.string().datetime(),
            idempotency_key: z.string().regex(/^[A-Za-z0-9_.:-]{1,128}$/).optional(),
          }),
        )
        .min(1)
        .max(100),
    },
    run: (a, api) => api.post("/v1/events", { events: a.events }),
  },
];
