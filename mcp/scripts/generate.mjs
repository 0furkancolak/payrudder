// Generates what the server reads from the repository: src/generated/read-tools.ts (the read tools, from api/openapi/v1.yaml and the allow-list in
// read-tools.config.mjs) and assets/ (the tenant API's OpenAPI document and the public guides, served as MCP resources). Both are build output (git-ignored): run by
// the pre* scripts of npm, so a tool or a resource can never be older than the API.
//   node scripts/generate.mjs
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { CHECKED_IDS, PERSONAL, READ_TOOLS, STRUCTURAL, UNTRUSTED } from "./read-tools.config.mjs";

const here = dirname(fileURLToPath(import.meta.url));
export const pkgRoot = resolve(here, "..");
export const repoRoot = resolve(pkgRoot, "..");

const snake = (s) => s.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
const deref = (doc, n) => (n?.$ref ? n.$ref.slice(2).split("/").reduce((x, k) => x?.[k.replace(/~1/g, "/").replace(/~0/g, "~")], doc) : n);

/** The GET operations of the tenant API by operationId. */
function getOperations(doc) {
  const out = new Map();
  for (const [path, item] of Object.entries(doc.paths ?? {})) {
    if (!path.startsWith("/v1/")) continue;
    for (const method of ["get", "post", "put", "patch", "delete"]) if (item[method]?.operationId) out.set(item[method].operationId, { method, path, item, op: item[method] });
  }
  return out;
}

/** The names of the string properties of a response that hold free text (no enum, pattern or identifier/time format), with the property they sit under for maps and lists. */
export function freeTextFields(doc, schema) {
  const found = new Set();
  // A $ref already on the way down ends the walk (a schema that contains itself).
  const visit = (s, key, seen) => {
    if (s?.$ref) {
      if (seen.has(s.$ref)) return;
      return visit(deref(doc, s), key, new Set([...seen, s.$ref]));
    }
    if (!s) return;
    for (const k of ["allOf", "oneOf", "anyOf"]) for (const x of s[k] ?? []) visit(x, key, seen);
    if (s.type === "object" || s.properties) {
      for (const [k, v] of Object.entries(s.properties ?? {})) visit(v, k, seen);
      if (s.additionalProperties && typeof s.additionalProperties === "object") visit(s.additionalProperties, key, seen);
    } else if (s.type === "array") visit(s.items, key, seen);
    else if (s.type === "string" && !s.enum && !s.pattern && !["uuid", "date-time", "date", "int64"].includes(s.format)) found.add(key);
  };
  visit(schema, "", new Set());
  return [...found].sort();
}

/**
 * The read tools: for each allow-listed operationId its name, path, arguments (from the operation's path and query parameters) and description (from its summary and
 * description). Throws, naming everything wrong, when the allow-list asks for something that is not a plain GET of the tenant API or an answer has an unclassified
 * free-text field.
 */
export function buildReadTools(doc, config = { READ_TOOLS, STRUCTURAL, UNTRUSTED, PERSONAL, CHECKED_IDS }) {
  const ops = getOperations(doc);
  const problems = [];
  const tools = [];
  const classified = new Set([...config.STRUCTURAL, ...config.UNTRUSTED]);
  for (const k of config.PERSONAL ?? []) if (!config.UNTRUSTED.includes(k)) problems.push(`${k} is personal data but not an untrusted key`);
  for (const both of config.STRUCTURAL) if (config.UNTRUSTED.includes(both)) problems.push(`${both} is both structural and untrusted`);
  for (const k of config.CHECKED_IDS ?? []) if (!config.STRUCTURAL.includes(k)) problems.push(`${k} is checked as an id but is not a structural key`);
  for (const [operationId, over] of Object.entries(config.READ_TOOLS)) {
    const o = ops.get(operationId);
    if (!o) {
      problems.push(`${operationId}: not an operation of the tenant API (/v1/*)`);
      continue;
    }
    if (o.method !== "get" || o.op.requestBody) {
      problems.push(`${operationId}: only a GET without a body can be a read tool`);
      continue;
    }
    const params = [...(o.item.parameters ?? []), ...(o.op.parameters ?? [])].map((p) => deref(doc, p));
    const spec = [];
    for (const p of params) {
      if (p.in === "header" || p.in === "cookie") {
        if (p.required) problems.push(`${operationId}: required ${p.in} parameter ${p.name}`);
        continue;
      }
      const s = deref(doc, p.schema) ?? {};
      if (s.type !== "string" && s.type !== "integer") {
        problems.push(`${operationId}: parameter ${p.name} is ${s.type}, not a string or an integer`);
        continue;
      }
      const arg = over.args?.[p.name] ?? p.name;
      spec.push({
        arg,
        name: p.name,
        in: p.in,
        required: p.in === "path" || p.required === true,
        type: s.type,
        ...(s.minimum !== undefined ? { min: s.minimum } : {}),
        ...(s.maximum !== undefined ? { max: s.maximum } : {}),
        ...(s.maxLength !== undefined ? { maxLength: s.maxLength } : {}),
        ...(s.pattern ? { pattern: s.pattern } : {}),
        ...(s.enum ? { enum: s.enum } : {}),
        ...(p.description ? { description: String(p.description).replace(/\s+/g, " ").trim() } : {}),
      });
    }
    for (const k of Object.keys(over.args ?? {})) if (!params.some((p) => p.name === k)) problems.push(`${operationId}: no parameter ${k} to rename`);
    if (new Set(spec.map((p) => p.arg)).size !== spec.length) problems.push(`${operationId}: two parameters became the same argument`);
    for (const p of o.path.matchAll(/\{([^}]+)\}/g)) if (!spec.some((s) => s.in === "path" && s.name === p[1])) problems.push(`${operationId}: path parameter ${p[1]} is not declared`);

    const ok = Object.entries(o.op.responses ?? {}).find(([c]) => c.startsWith("2"));
    const schema = deref(doc, ok?.[1])?.content?.["application/json"]?.schema;
    if (!schema) problems.push(`${operationId}: no JSON answer`);
    else for (const f of freeTextFields(doc, schema)) if (f && !classified.has(f)) problems.push(`${operationId}: the answer has a free-text field "${f}" that is neither untrusted nor structural (read-tools.config.mjs)`);

    let paragraph = String(o.op.description ?? "").trim().split(/\n\s*\n/)[0]?.replace(/\s+/g, " ") ?? "";
    // A description that says "see `getEntitlement`" must name a tool the model has: the name of an allow-listed operation becomes its tool name, any other operation is an error.
    paragraph = paragraph.replace(/`([A-Za-z][A-Za-z0-9]*)`/g, (whole, id) => {
      if (!ops.has(id)) return whole;
      if (id in config.READ_TOOLS) return `\`${snake(id)}\``;
      problems.push(`${operationId}: its description names ${id}, which is not a tool (allow-list it or reword the description)`);
      return whole;
    });
    const summary = String(o.op.summary ?? operationId);
    tools.push({
      name: snake(operationId),
      operationId,
      path: o.path,
      title: summary,
      description: [summary.replace(/\.$/, "") + ".", paragraph, spec.some((p) => p.arg === "page_token") ? "Use next_page_token for the next page." : ""].filter(Boolean).join(" "),
      params: spec,
    });
  }
  if (problems.length) throw new Error(`read tools: ${problems.join("; ")}`);
  return { tools, untrusted: [...config.UNTRUSTED], personal: [...(config.PERSONAL ?? [])], checkedIds: [...(config.CHECKED_IDS ?? [])] };
}

/** The tenant API as its own OpenAPI document: the /v1 operations and exactly the components they reach, with the API-key scheme and no operator or console route. */
export function tenantSubset(doc) {
  const paths = Object.fromEntries(Object.entries(doc.paths ?? {}).filter(([p]) => p.startsWith("/v1/")));
  const components = {};
  const seen = new Set();
  const take = (node) => {
    if (!node || typeof node !== "object") return;
    if (typeof node.$ref === "string" && node.$ref.startsWith("#/components/")) {
      if (seen.has(node.$ref)) return;
      seen.add(node.$ref);
      const [, , kind, name] = node.$ref.split("/");
      const target = doc.components?.[kind]?.[name];
      if (target === undefined) throw new Error(`dangling reference ${node.$ref}`);
      (components[kind] ??= {})[name] = target;
      return take(target);
    }
    for (const v of Object.values(node)) take(v);
  };
  take(paths);
  const apiKey = doc.components?.securitySchemes?.apiKey;
  if (apiKey) components.securitySchemes = { apiKey };
  return { openapi: doc.openapi, info: doc.info, servers: doc.servers, security: [{ apiKey: [] }], paths, components };
}

/** The page name of a guide file: 16-webhooks.md is "webhooks". */
const pageOf = (file) => file.replace(/\.md$/, "").replace(/^\d+-/, "");

/** The guides an assistant can read: the site's own pages (docs-site/content) and the documents docs/publication.yaml marks public. */
export async function publicGuides() {
  const out = [];
  const own = join(repoRoot, "docs-site/content");
  for (const f of (await readdir(own).catch(() => [])).filter((x) => x.endsWith(".md")).sort()) out.push({ page: pageOf(f), file: join(own, f) });
  const manifest = parse(await readFile(join(repoRoot, "docs/publication.yaml"), "utf8"));
  for (const [f, m] of Object.entries(manifest.files ?? {})) if (m.audience === "public") out.push({ page: pageOf(f), file: join(repoRoot, "docs", f) });
  return out;
}

const titleOf = (md, fallback) => /^#\s+(.+)$/m.exec(md)?.[1]?.trim() ?? fallback;

async function generateAssets(doc) {
  const dir = join(pkgRoot, "assets");
  await rm(dir, { recursive: true, force: true });
  await mkdir(join(dir, "docs"), { recursive: true });
  const index = [];
  await writeFile(join(dir, "openapi-v1.json"), JSON.stringify(tenantSubset(doc)));
  index.push({
    uri: "payrudder://openapi",
    name: "openapi",
    title: "Payrudder tenant API (OpenAPI 3.1)",
    description: "Every operation of the tenant API (/v1/*, authenticated with an API key), with its parameters, request and response models.",
    mimeType: "application/json",
    file: "openapi-v1.json",
  });
  for (const g of await publicGuides()) {
    const md = await readFile(g.file, "utf8");
    await writeFile(join(dir, "docs", `${g.page}.md`), md);
    index.push({ uri: `payrudder://docs/${g.page}`, name: `docs-${g.page}`, title: titleOf(md, g.page), description: `The ${g.page} guide of the developer documentation.`, mimeType: "text/markdown", file: `docs/${g.page}.md` });
  }
  await writeFile(join(dir, "index.json"), JSON.stringify(index, null, 2));
  return index.length;
}

async function generateTools(doc) {
  const { tools, untrusted, personal, checkedIds } = buildReadTools(doc);
  const file = join(pkgRoot, "src/generated/read-tools.ts");
  await mkdir(dirname(file), { recursive: true });
  await writeFile(
    file,
    `// Generated by scripts/generate.mjs from api/openapi/v1.yaml and scripts/read-tools.config.mjs. Do not edit; it is not committed.
import type { ReadToolSpec } from "../readTools.js";

export const READ_TOOL_SPECS: ReadToolSpec[] = ${JSON.stringify(tools, null, 2)};

export const UNTRUSTED_KEYS: string[] = ${JSON.stringify(untrusted)};

export const PERSONAL_KEYS: string[] = ${JSON.stringify(personal)};

export const CHECKED_ID_KEYS: string[] = ${JSON.stringify(checkedIds)};
`,
  );
  return tools.length;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const doc = parse(await readFile(join(repoRoot, "api/openapi/v1.yaml"), "utf8"));
  const n = await generateTools(doc);
  const r = await generateAssets(doc);
  console.log(`mcp: ${n} read tools and ${r} resources generated`);
}
