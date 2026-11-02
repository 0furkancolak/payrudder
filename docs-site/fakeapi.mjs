// A stand-in for the Payrudder API that is made from api/openapi/v1.yaml: it checks that the request is one the file describes (a known route and verb, a bearer key, a body
// that fits the request schema) and answers with the file's own example of the success response. The snippets of the Connect page and the framework templates under
// examples/ run against it in CI, so they are executed without a stack. It proves that what they send is what the API's contract asks for; it does not prove that the real
// gateway accepts it (no database, no key check, no business rule).
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { exampleOf, operationsOf } from "./build.mjs";

const here = dirname(fileURLToPath(import.meta.url));

export async function loadSpec() {
  return parse(await readFile(resolve(here, "../api/openapi/v1.yaml"), "utf8"));
}

const resolveRef = (doc, ref) => ref.slice(2).split("/").reduce((n, k) => n?.[k], doc);

/** Problems of a JSON value against a schema of the OpenAPI file; [] when it fits. Covers what request bodies use: $ref, type, required, properties, items, enum, oneOf, min/max, pattern, formats. */
export function check(doc, schema, value, at = "$") {
  if (schema?.$ref) return check(doc, resolveRef(doc, schema.$ref), value, at);
  if (!schema) return [];
  if (schema.oneOf) return schema.oneOf.some((s) => check(doc, s, value, at).length === 0) ? [] : [`${at}: fits none of ${schema.oneOf.length} shapes`];
  if (schema.allOf) return schema.allOf.flatMap((s) => check(doc, s, value, at));
  const problems = [];
  const type = Array.isArray(value) ? "array" : value === null ? "null" : typeof value;
  if (schema.nullable && value === null) return [];
  if (schema.type === "integer" ? !Number.isInteger(value) : schema.type && schema.type !== type) return [`${at}: expected ${schema.type}, got ${type}`];
  if (schema.enum && !schema.enum.includes(value)) problems.push(`${at}: ${JSON.stringify(value)} is not one of ${schema.enum.join(", ")}`);
  if (type === "string") {
    if (schema.pattern && !new RegExp(schema.pattern).test(value)) problems.push(`${at}: does not match ${schema.pattern}`);
    if (schema.maxLength !== undefined && value.length > schema.maxLength) problems.push(`${at}: longer than ${schema.maxLength}`);
    if (schema.format === "date-time" && !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/.test(value)) problems.push(`${at}: not an RFC 3339 instant`);
    if (schema.format === "email" && !/^[^@\s]+@[^@\s]+$/.test(value)) problems.push(`${at}: not an e-mail address`);
  }
  if (type === "array") {
    if (schema.minItems !== undefined && value.length < schema.minItems) problems.push(`${at}: fewer than ${schema.minItems} items`);
    if (schema.maxItems !== undefined && value.length > schema.maxItems) problems.push(`${at}: more than ${schema.maxItems} items`);
    value.forEach((v, i) => problems.push(...check(doc, schema.items, v, `${at}[${i}]`)));
  }
  if (type === "object") {
    for (const k of schema.required ?? []) if (!(k in value)) problems.push(`${at}: ${k} is required`);
    for (const [k, v] of Object.entries(value)) if (schema.properties?.[k]) problems.push(...check(doc, schema.properties[k], v, `${at}.${k}`));
  }
  return problems;
}

/** The success answer of an operation: its first 2xx response, as the file's example (one result per event for usage events, which is what a client indexes). */
function answerOf(doc, o, body) {
  const [code, resp] = Object.entries(o.op.responses ?? {}).find(([c]) => c.startsWith("2")) ?? ["204", {}];
  const schema = (resp.$ref ? resolveRef(doc, resp.$ref) : resp).content?.["application/json"]?.schema;
  if (!schema) return { status: Number(code), json: undefined };
  const json = exampleOf(doc, schema);
  if (o.id === "ingestEvents") json.results = (body?.events ?? []).map((_, i) => ({ status: "accepted", event_id: `evt_fake_${i + 1}` }));
  return { status: Number(code), json };
}

/**
 * Starts the fake on a free port of 127.0.0.1. `seen` collects every request: { method, path, auth, body, problems }, where `problems` is what the contract objects to (an empty
 * array when the request fits). A request the file does not describe is answered 404 and recorded with a problem.
 */
export async function startFakeApi(doc) {
  const ops = operationsOf(doc);
  const seen = [];
  const server = createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const path = (req.url ?? "").split("?")[0];
      const text = Buffer.concat(chunks).toString();
      const o = ops.find((x) => x.method === req.method && new RegExp(`^${x.path.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(path));
      const entry = { method: req.method, path, auth: req.headers.authorization, text, body: undefined, problems: [] };
      seen.push(entry);
      if (!o) {
        entry.problems.push(`${req.method} ${path} is not an operation of the OpenAPI file`);
        res.writeHead(404, { "content-type": "application/json" }).end(JSON.stringify({ error: { key: "gateway.not_found" } }));
        return;
      }
      if (!/^Bearer \S+$/.test(entry.auth ?? "")) entry.problems.push("no bearer key");
      const schema = o.op.requestBody?.content?.["application/json"]?.schema;
      if (schema) {
        try {
          entry.body = JSON.parse(text);
          entry.problems.push(...check(doc, schema, entry.body));
        } catch {
          entry.problems.push("the body is not JSON");
        }
      }
      const a = answerOf(doc, o, entry.body);
      res.writeHead(entry.problems.length ? 400 : a.status, { "content-type": "application/json" });
      res.end(entry.problems.length ? JSON.stringify({ error: { key: "gateway.bad_request", message: entry.problems.join("; ") } }) : a.json === undefined ? undefined : JSON.stringify(a.json));
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}`;
  return { url, seen, close: () => new Promise((r) => server.close(r)) };
}

export const repoRoot = resolve(here, "..");
export const inRepo = (...p) => join(repoRoot, ...p);
