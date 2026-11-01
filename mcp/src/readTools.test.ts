import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import { buildReadTools, freeTextFields, repoRoot } from "../scripts/generate.mjs";
import { READ_TOOLS, STRUCTURAL, UNTRUSTED } from "../scripts/read-tools.config.mjs";
import { READ_TOOL_SPECS } from "./generated/read-tools.js";
import { TOOLS } from "./tools.js";

const doc = parse(await readFile(join(repoRoot, "api/openapi/v1.yaml"), "utf8"));
const config = { READ_TOOLS, STRUCTURAL, UNTRUSTED };

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
    expect(READ_TOOL_SPECS.map((t) => t.name).sort()).toEqual(["get_customer", "get_customer_usage", "get_invoice", "list_customers", "list_invoices", "list_meters", "list_payments", "list_plans", "list_subscriptions"]);
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

  it("classifies every free-text field of every answer, as untrusted or as an identifier", () => {
    for (const t of READ_TOOL_SPECS) {
      const o = operation(t.operationId) as unknown as { op: { responses: Record<string, unknown> } };
      const ok = Object.entries(o.op.responses).find(([c]) => c.startsWith("2"))![1] as { content: { "application/json": { schema: unknown } } };
      for (const f of freeTextFields(doc, ok.content["application/json"].schema)) expect([...STRUCTURAL, ...UNTRUSTED], `${t.name}: ${f}`).toContain(f);
    }
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
  it("refuses an answer with a free-text field nobody classified, and accepts it once it is classified", () => {
    expect(() => buildReadTools(spec({}, { comment: { type: "string" } }), cfg({ getThing: {} }))).toThrow(/free-text field "comment"/);
    expect(buildReadTools(spec({}, { note: { type: "string" } }), cfg({ getThing: {} })).tools).toHaveLength(1);
    expect(buildReadTools(spec({}, { when: { type: "string", format: "date-time" }, kind: { type: "string", enum: ["a"] } }), cfg({ getThing: {} })).tools).toHaveLength(1);
  });
});
