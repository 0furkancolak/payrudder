import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import YAML from "yaml";
import { Payrudder, VERSION, WebhookEventTypes } from "../src/index";
import { generate } from "../scripts/generate.mjs";

const root = resolve(import.meta.dirname, "..");
const specText = readFileSync(resolve(root, "../../api/openapi/v1.yaml"), "utf8");

interface Spec {
  paths: Record<string, Record<string, { operationId?: string }>>;
  components: { schemas: { WebhookEventType: { enum: string[] } } };
}
const spec = YAML.parse(specText) as Spec;

const camel = (s: string) => s.replace(/-([a-z])/g, (_m, c: string) => c.toUpperCase());

// Hand-written helpers that are not an operationId.
const HELPERS = new Set(["ingest"]);

const operations: Array<{ id: string; ns: string; path: string }> = [];
for (const [path, item] of Object.entries(spec.paths)) {
  if (!path.startsWith("/v1/")) continue;
  for (const [method, op] of Object.entries(item)) {
    if (!["get", "post", "put", "patch", "delete"].includes(method)) continue;
    operations.push({ id: op.operationId ?? `MISSING ${method} ${path}`, ns: camel(path.split("/")[2] ?? ""), path });
  }
}

describe("SDK stays in sync with api/openapi/v1.yaml", () => {
  const client = new Payrudder({ apiKey: "k" }) as unknown as Record<string, Record<string, unknown>>;

  it("sees the tenant API", () => {
    expect(operations.length).toBeGreaterThan(100);
    expect(new Set(operations.map((o) => o.id)).size).toBe(operations.length); // operationIds are unique
  });

  it("has a client method for every /v1 operationId, in the namespace of its first path segment", () => {
    const missing = operations.filter((o) => typeof client[o.ns]?.[o.id] !== "function").map((o) => `${o.ns}.${o.id} (${o.path})`);
    expect(missing).toEqual([]);
  });

  it("has no client method that is not an operationId (besides the documented helpers)", () => {
    const ids = new Set(operations.map((o) => o.id));
    const extra: string[] = [];
    for (const [ns, resource] of Object.entries(client)) {
      if (ns === "paginate" || typeof resource !== "object") continue;
      for (let proto = Object.getPrototypeOf(resource) as object | null; proto && proto !== Object.prototype; proto = Object.getPrototypeOf(proto) as object | null) {
        for (const name of Object.getOwnPropertyNames(proto)) {
          if (name !== "constructor" && !ids.has(name) && !HELPERS.has(name)) extra.push(`${ns}.${name}`);
        }
      }
    }
    expect(extra).toEqual([]);
  });

  it("only offers paginators for real operations", () => {
    const ids = new Set(operations.map((o) => o.id));
    const names = Object.getOwnPropertyNames(Object.getPrototypeOf(client.paginate)).filter((n) => n !== "constructor");
    expect(names.length).toBeGreaterThan(10);
    expect(names.filter((n) => !ids.has(n))).toEqual([]);
  });

  it("checked-in generated code is up to date (run `npm run generate`)", () => {
    const { files } = generate(specText) as { files: Record<string, string> };
    for (const [rel, content] of Object.entries(files)) {
      expect(readFileSync(resolve(root, rel), "utf8"), rel).toBe(content);
    }
  });

  it("webhook event types equal the spec enum and docs/16-webhooks.md", () => {
    const sdk = Object.values(WebhookEventTypes).filter((t) => t !== "ping");
    expect([...sdk].sort()).toEqual([...spec.components.schemas.WebhookEventType.enum].sort());

    const docs = readFileSync(resolve(root, "../../docs/16-webhooks.md"), "utf8");
    const table = docs.slice(docs.indexOf("## Event types"), docs.indexOf("A delivery is a JSON"));
    const documented = new Set<string>();
    for (const row of table.split("\n").filter((l) => l.startsWith("| `"))) {
      const cell = row.split("|")[1] ?? "";
      for (const m of cell.matchAll(/`([a-z_]+\.[a-z_]+)`/g)) documented.add(m[1] as string);
    }
    expect([...sdk].sort()).toEqual([...documented].sort());
  });

  it("package version matches VERSION", () => {
    const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8")) as { version: string };
    expect(pkg.version).toBe(VERSION);
    expect(VERSION).toBe("0.0.0");
  });
});
