import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import { buildReadTools, freeTextFields, repoRoot } from "../scripts/generate.mjs";
import { NOT_OFFERED, PERSONAL, READ_TOOLS, STRUCTURAL, UNTRUSTED } from "../scripts/read-tools.config.mjs";
import { PERSONAL_KEYS, READ_TOOL_SPECS, UNTRUSTED_KEYS } from "./generated/read-tools.js";
import { REDACTED, redactPersonal } from "./privacy.js";
import { TOOLS } from "./tools.js";

const doc = parse(await readFile(join(repoRoot, "api/openapi/v1.yaml"), "utf8"));
const config = { READ_TOOLS, STRUCTURAL, UNTRUSTED, PERSONAL };

interface Op {
  operationId?: string;
  parameters?: Param[];
  requestBody?: unknown;
}
interface Param {
  $ref?: string;
  name: string;
  in: string;
  required?: boolean;
  schema?: { type?: string; minimum?: number; maximum?: number; enum?: string[]; pattern?: string };
}
const deref = (p: Param): Param => (p.$ref ? (p.$ref.slice(2).split("/").reduce((n: any, k) => n[k], doc) as Param) : p);
function operation(id: string): { path: string; method: string; op: Op; params: Param[] } {
  for (const [path, item] of Object.entries<any>(doc.paths))
    for (const method of ["get", "post", "put", "patch", "delete"]) if (item[method]?.operationId === id) return { path, method, op: item[method], params: [...(item.parameters ?? []), ...(item[method].parameters ?? [])].map(deref) };
  throw new Error(`no operation ${id}`);
}

describe("the read tools are the allow-listed operations of the OpenAPI file", () => {
  it("has one tool per allow-listed operation, on a GET route of the tenant API, with the names the first tools had", () => {
    expect(READ_TOOL_SPECS.map((t) => t.operationId)).toEqual(Object.keys(READ_TOOLS));
    // Written out on purpose: a tool an assistant can call is a decision, so adding or dropping one must change this list (and the risk table in README.md).
    expect(READ_TOOL_SPECS.map((t) => t.name).sort()).toEqual([
      "get_credit_balance", "get_credit_note", "get_customer", "get_customer_usage", "get_dunning_report", "get_entitlement", "get_invoice", "get_meter", "get_plan_version", "get_revenue_report", "get_subscription",
      "get_subscription_report", "get_tax_rates", "get_upcoming_invoice", "list_coupons", "list_credit_ledger", "list_credit_notes", "list_customer_payments", "list_customers", "list_entitlements",
      "list_invoices", "list_meters", "list_payments", "list_plans", "list_subscriptions",
    ]);
    for (const t of READ_TOOL_SPECS) {
      const o = operation(t.operationId);
      expect(o.method, t.name).toBe("get");
      expect(o.path.startsWith("/v1/"), t.name).toBe(true);
      expect(o.path, t.name).toBe(t.path);
      expect(o.op.requestBody, t.name).toBeUndefined();
    }
  });

  it("takes exactly the path and query parameters of the operation, with its limits, under the argument names of the allow-list", () => {
    for (const t of READ_TOOL_SPECS) {
      const o = operation(t.operationId);
      const wanted = o.params.filter((p) => p.in === "path" || p.in === "query");
      expect(t.params.map((p) => p.name), t.name).toEqual(wanted.map((p) => p.name));
      for (const p of t.params) {
        const api = wanted.find((w) => w.name === p.name)!;
        expect(p.arg, `${t.name}.${p.name}`).toBe((READ_TOOLS as Record<string, { args?: Record<string, string> }>)[t.operationId]?.args?.[p.name] ?? p.name);
        expect(p.in).toBe(api.in);
        expect(p.required, `${t.name}.${p.name}`).toBe(api.in === "path" || api.required === true);
        expect(p.type).toBe(api.schema?.type);
        expect(p.min).toBe(api.schema?.minimum);
        expect(p.max).toBe(api.schema?.maximum);
        expect(p.enum).toEqual(api.schema?.enum);
        expect(p.pattern).toBe(api.schema?.pattern);
      }
    }
  });

  it("is what the generator makes from the file as it is now (a stale generated file fails here)", () => {
    expect(buildReadTools(doc, config).tools).toEqual(READ_TOOL_SPECS);
  });

  it("offers no tool that is not a read of the allow-list except the two that write", () => {
    expect(TOOLS.filter((t) => !t.write).map((t) => t.name)).toEqual(READ_TOOL_SPECS.map((t) => t.name));
    expect(TOOLS.filter((t) => t.write).map((t) => t.name)).toEqual(["create_customer", "ingest_usage_events"]);
  });

  it("keeps the first nine tools, under the names and arguments an assistant's saved prompts use", () => {
    const first = Object.fromEntries(READ_TOOL_SPECS.map((t) => [t.name, t.params.map((p) => p.arg)]));
    expect(first.get_customer).toEqual(["customer_external_id"]);
    expect(first.get_customer_usage).toEqual(["customer_external_id", "period"]);
    expect(first.list_subscriptions).toContain("customer_external_id");
    expect(first.list_invoices).toContain("customer_external_id");
    expect(first.get_invoice).toEqual(["invoice_id"]);
  });

  it("offers none of the reads that were left out on purpose, and each of them is still a GET of the file", () => {
    for (const [id, why] of Object.entries(NOT_OFFERED)) {
      expect(Object.keys(READ_TOOLS), id).not.toContain(id);
      expect(operation(id).method, id).toBe("get");
      expect(why.length, id).toBeGreaterThan(10);
    }
  });

  it("offers no read whose answer is a link, a secret or a raw payload (by the names the API gives such fields)", () => {
    const risky = (f: string) => f !== "next_page_token" && f.split("_").some((w) => ["secret", "token", "url", "link", "presigned", "plaintext", "payload", "password", "authorization", "iban", "pan", "cvv"].includes(w));
    for (const t of READ_TOOL_SPECS) {
      const o = operation(t.operationId) as unknown as { op: { responses: Record<string, unknown> } };
      const ok = Object.entries(o.op.responses).find(([c]) => c.startsWith("2"))![1] as { content: { "application/json": { schema: unknown } } };
      const fields = new Set<string>();
      const seen = new Set<string>();
      const walk = (n: any): void => {
        if (!n || typeof n !== "object") return;
        if (n.$ref) {
          if (seen.has(n.$ref)) return;
          seen.add(n.$ref);
          return walk(n.$ref.slice(2).split("/").reduce((x: any, k: string) => x?.[k], doc));
        }
        for (const [k, v] of Object.entries<any>(n.properties ?? {})) {
          fields.add(k);
          walk(v);
        }
        for (const k of ["items", "additionalProperties"]) walk(n[k]);
        for (const k of ["allOf", "oneOf", "anyOf"]) for (const x of n[k] ?? []) walk(x);
      };
      walk(ok.content["application/json"].schema);
      expect([...fields].filter(risky), t.name).toEqual([]);
    }
  });

  it("classifies every free-text field of every answer, as untrusted or as an identifier", () => {
    for (const t of READ_TOOL_SPECS) {
      const o = operation(t.operationId) as unknown as { op: { responses: Record<string, unknown> } };
      const ok = Object.entries(o.op.responses).find(([c]) => c.startsWith("2"))![1] as { content: { "application/json": { schema: unknown } } };
      for (const f of freeTextFields(doc, ok.content["application/json"].schema)) expect([...STRUCTURAL, ...UNTRUSTED], `${t.name}: ${f}`).toContain(f);
    }
  });
});

describe("personal data", () => {
  it("is classified: every personal key is an untrusted key, and the generated list is the configured one", () => {
    for (const k of PERSONAL) expect(UNTRUSTED, k).toContain(k);
    expect(PERSONAL_KEYS).toEqual(PERSONAL);
  });

  it("is redacted in full from a customer: every text of the Customer answer that people wrote, but the staff's block note and the id warning", () => {
    // A sample with a text in every string property of the Customer model, so a field added to it (and to UNTRUSTED) without a decision about personal data fails here.
    const sample = (n: any): unknown => {
      if (n?.$ref) return sample(n.$ref.slice(2).split("/").reduce((x: any, k: string) => x?.[k], doc));
      if (n?.properties) return Object.fromEntries(Object.entries<any>(n.properties).map(([k, v]) => [k, sample(v)]));
      return n?.type === "string" ? "text" : null;
    };
    const customer = sample({ $ref: "#/components/schemas/Customer" }) as Record<string, any>;
    const { value } = redactPersonal(customer, new Set(PERSONAL));
    const left: string[] = [];
    const walk = (v: unknown, path: string): void => {
      if (typeof v === "string") {
        const key = path.split(".").pop() ?? "";
        if (v !== REDACTED && UNTRUSTED.includes(key)) left.push(path);
      } else if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) walk(x, path ? `${path}.${k}` : k);
    };
    walk(value, "");
    // Staff's own words stay (they explain a block); nothing else people wrote may.
    expect(left.sort()).toEqual(["block_reason", "tax_id_warning"]);
    expect(UNTRUSTED_KEYS).toContain("block_reason");
  });
});

describe("the generator refuses what must not become a tool", () => {
  const spec = (over: Record<string, unknown> = {}, extra: Record<string, unknown> = {}) => ({
    paths: {
      "/v1/things/{id}": { get: { operationId: "getThing", parameters: [{ name: "id", in: "path", required: true, schema: { type: "string" } }], responses: { "200": { description: "OK", content: { "application/json": { schema: { type: "object", properties: { id: { type: "string" }, ...extra } } } } } }, ...over } },
      "/v1/things": { post: { operationId: "createThing", requestBody: {}, responses: { "200": { description: "OK" } } } },
      "/admin/v1/tenants": { get: { operationId: "adminListTenants", responses: { "200": { description: "OK" } } } },
    },
  });
  const cfg = (tools: Record<string, object>) => ({ READ_TOOLS: tools, STRUCTURAL: ["id"], UNTRUSTED: ["note"] });

  it("refuses a personal key that is not an untrusted key", () => {
    expect(() => buildReadTools(spec(), { ...cfg({ getThing: {} }), PERSONAL: ["email"] })).toThrow(/email is personal data but not an untrusted key/);
    expect(buildReadTools(spec(), { ...cfg({ getThing: {} }), PERSONAL: ["note"] }).personal).toEqual(["note"]);
  });
  it("accepts a plain GET of the tenant API", () => {
    expect(buildReadTools(spec(), cfg({ getThing: {} })).tools[0]).toMatchObject({ name: "get_thing", path: "/v1/things/{id}" });
  });
  it("refuses a write, an operator route and an operation that does not exist", () => {
    expect(() => buildReadTools(spec(), cfg({ createThing: {} }))).toThrow(/createThing/);
    expect(() => buildReadTools(spec(), cfg({ adminListTenants: {} }))).toThrow(/adminListTenants/);
    expect(() => buildReadTools(spec(), cfg({ vanished: {} }))).toThrow(/vanished/);
  });
  it("refuses a rename of a parameter that is not there", () => {
    expect(() => buildReadTools(spec(), cfg({ getThing: { args: { nope: "x" } } }))).toThrow(/no parameter nope/);
  });
  it("renames an allow-listed operation named in a description to its tool, and refuses one that is not a tool", () => {
    const ok = { "200": { description: "OK", content: { "application/json": { schema: { type: "object", properties: { id: { type: "string" } } } } } } };
    const withText = (description: string) => {
      const s = spec({ description }) as { paths: Record<string, unknown> };
      const other = { get: { operationId: "getOtherThing", parameters: [{ name: "id", in: "path", required: true, schema: { type: "string" } }], responses: ok } };
      return { ...s, paths: { ...s.paths, "/v1/other/{id}": other } };
    };
    expect(buildReadTools(withText("Like `getOtherThing`, but one."), cfg({ getThing: {}, getOtherThing: {} })).tools[0]?.description).toContain("Like `get_other_thing`, but one.");
    expect(() => buildReadTools(withText("Like `getOtherThing`, but one."), cfg({ getThing: {} }))).toThrow(/names getOtherThing, which is not a tool/);
    expect(buildReadTools(withText("Counts `quantity` as text."), cfg({ getThing: {} })).tools[0]?.description).toContain("`quantity`");
  });
  it("refuses an answer with a free-text field nobody classified, and accepts it once it is classified", () => {
    expect(() => buildReadTools(spec({}, { comment: { type: "string" } }), cfg({ getThing: {} }))).toThrow(/free-text field "comment"/);
    expect(buildReadTools(spec({}, { note: { type: "string" } }), cfg({ getThing: {} })).tools).toHaveLength(1);
    expect(buildReadTools(spec({}, { when: { type: "string", format: "date-time" }, kind: { type: "string", enum: ["a"] } }), cfg({ getThing: {} })).tools).toHaveLength(1);
  });
});
