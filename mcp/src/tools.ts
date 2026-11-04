import { z } from "zod";
import type { Api } from "./api.js";
import { READ_TOOL_SPECS } from "./generated/read-tools.js";
import { toolOf } from "./readTools.js";

/**
 * The tools the server offers. Every one is a thin call of a tenant API route: what a key may do is decided by the key and the API, not here. The reads are generated
 * from api/openapi/v1.yaml (scripts/generate.mjs, allow-list in scripts/read-tools.config.mjs) and are always on; a tool that writes (`write: true`) is written by hand
 * below, is offered only when the server is started with writes enabled, and says so in its description.
 */
export interface ToolDef {
  name: string;
  title: string;
  description: string;
  input: z.ZodRawShape;
  write?: boolean;
  /** The API permission the call needs (customers:read); the OAuth mode refuses a token without it with `403 insufficient_scope` before the tool runs. Absent: the API decides. */
  scope?: string;
  run(args: Record<string, unknown>, api: Api): Promise<unknown>;
}

const customer = z.string().min(1).max(200);

export const TOOLS: ToolDef[] = [
  ...READ_TOOL_SPECS.map(toolOf),
  {
    name: "create_customer",
    title: "Create a customer",
    description: "WRITES. Creates a customer (external id, name and e-mail are required). Fails if the external id exists.",
    write: true,
    scope: "customers:write",
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
    scope: "events:write",
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
