import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, readdir, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { parse } from "yaml";
import { EVENT_CODES, GUIDES, OPERATOR_AREAS, STATUSES, TABBED, build, errorKeys, exampleOf, loadPublication, meaningsOf, operationsOf, publicationOf } from "./build.mjs";
import { LANGS } from "../web/admin/src/features/connect/snippets.ts";

const here = dirname(fileURLToPath(import.meta.url));
const doc = parse(await readFile(resolve(here, "../api/openapi/v1.yaml"), "utf8"));
const out = await mkdtemp(join(tmpdir(), "docs-"));
const built = await build(out);

async function pages() {
  const all = [];
  for (const f of await readdir(out)) if (f.endsWith(".html")) all.push(f);
  for (const f of await readdir(join(out, "api"))) all.push(`api/${f}`);
  return all;
}

test("every operation of the tenant API has its anchor on the page of its group", async () => {
  const ops = operationsOf(doc);
  assert.ok(ops.length > 100);
  assert.equal(built.operations, ops.length);
  const cache = new Map();
  for (const o of ops) {
    const page = join(out, "api", `${o.group}.html`);
    if (!cache.has(page)) cache.set(page, await readFile(page, "utf8"));
    assert.ok(cache.get(page).includes(`id="${o.id}"`), `${o.method} ${o.path} (${o.id}) is missing`);
  }
});

test("only the tenant API is documented: no operation of the operator, console or sign-in routes has an entry", async () => {
  const hidden = [];
  for (const [path, item] of Object.entries(doc.paths)) {
    if (path.startsWith("/v1/")) continue;
    for (const m of ["get", "post", "put", "patch", "delete"]) if (item[m]?.operationId) hidden.push(item[m].operationId);
  }
  assert.ok(hidden.length > 50);
  for (const f of (await pages()).filter((x) => x.startsWith("api/"))) {
    const html = await readFile(join(out, f), "utf8");
    for (const id of hidden) assert.ok(!html.includes(`id="${id}"`), `${f} documents ${id}`);
    assert.ok(!/<h3><span class="m [^>]*>[A-Z]+<\/span> <code>\/(admin|app|auth)\//.test(html), `${f} has an entry for a non-tenant route`);
  }
});

test("no page shows a broken template value, and every internal link points at a page or an anchor that exists", async () => {
  const ids = new Map();
  const html = new Map();
  for (const f of await pages()) {
    const h = await readFile(join(out, f), "utf8");
    html.set(f, h);
    ids.set(f, new Set([...h.matchAll(/id="([^"]+)"/g)].map((m) => m[1])));
    assert.ok(!/undefined|\[object Object\]|\bNaN\b/.test(h.replace(/<script[\s\S]*?<\/script>/g, "").replace(/<style[\s\S]*?<\/style>/g, "").replace(/<pre[\s\S]*?<\/pre>/g, "")), `${f} shows a broken value`);
  }
  for (const [f, h] of html) {
    for (const m of h.matchAll(/<a [^>]*href="([^"]+)"/g)) {
      const href = m[1];
      if (/^(https?:|mailto:)/.test(href) || href === "#") continue;
      const [target, frag] = href.split("#");
      const file = target === "" ? f : join(dirname(f), target).replace(/\\/g, "/");
      assert.ok(existsSync(join(out, file)), `${f}: ${href} -> ${file} does not exist`);
      if (frag) assert.ok(ids.get(file)?.has(frag), `${f}: ${href} has no anchor ${frag}`);
    }
  }
});

test("the example request bodies are valid JSON and the curl examples never carry a real key", async () => {
  const events = await readFile(join(out, "api", "events.html"), "utf8");
  assert.match(events, /\$PAYRUDDER_API_KEY/);
  assert.ok(!/qk_(live|test)_[A-Za-z0-9]{8,}/.test(events));
  const body = doc.paths["/v1/events"].post.requestBody.content["application/json"].schema;
  const ex = exampleOf(doc, body);
  assert.doesNotThrow(() => JSON.parse(JSON.stringify(ex)));
  assert.ok(Array.isArray(ex.events) && ex.events[0].meter_code !== undefined);
});

test("a schema that refers to itself ends instead of looping", () => {
  const cyc = { components: { schemas: { A: { type: "object", required: ["a"], properties: { a: { $ref: "#/components/schemas/A" } } } } } };
  assert.doesNotThrow(() => exampleOf(cyc, { $ref: "#/components/schemas/A" }));
});

test("the search index has a page for every guide and an entry per operation", async () => {
  const idx = JSON.parse(await readFile(join(out, "search.json"), "utf8"));
  assert.ok(idx.length >= built.operations + 4);
  assert.ok(idx.some((e) => e.url === "webhooks.html"));
  assert.ok(idx.some((e) => e.title === "POST /v1/events"));
});

// ---- what is published, and how finished it is (plan 19)

const docsDir = resolve(here, "../docs");
const pub = await loadPublication();

async function markdownFiles(dir, prefix = "") {
  const all = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    if (e.isDirectory()) all.push(...(await markdownFiles(join(dir, e.name), `${prefix}${e.name}/`)));
    else if (e.name.endsWith(".md")) all.push(`${prefix}${e.name}`);
  }
  return all;
}

test("every document of docs/ is marked public or internal, with a status: a new one fails here until someone decides", async () => {
  const audiences = new Set(["public", "internal"]);
  const missing = [];
  for (const f of await markdownFiles(docsDir)) {
    const m = publicationOf(pub, f);
    if (!m) missing.push(f);
    else {
      assert.ok(audiences.has(m.audience), `${f}: audience ${m.audience}`);
      assert.ok(["stable", "preview", "pre-release", "draft", "historical"].includes(m.status), `${f}: status ${m.status}`);
    }
  }
  assert.deepEqual(missing, [], `add a line to docs/publication.yaml for: ${missing.join(", ")}`);
});

test("docs/publication.yaml lists only what exists", async () => {
  for (const f of Object.keys(pub.files)) assert.ok(existsSync(join(docsDir, f)), `${f} is listed but missing`);
  for (const d of Object.keys(pub.directories)) assert.ok((await stat(join(docsDir, d))).isDirectory(), `${d} is not a directory`);
});

test("what the site publishes from docs/ is public, and every public document is published", async () => {
  const published = new Set();
  for (const g of GUIDES) for (const f of [g.src, ...(g.also ?? [])]) if (f.startsWith(`${docsDir}/`)) published.add(f.slice(docsDir.length + 1));
  for (const f of published) assert.equal(publicationOf(pub, f)?.audience, "public", `${f} is published but not marked public`);
  const isPublic = [];
  for (const f of await markdownFiles(docsDir)) if (publicationOf(pub, f)?.audience === "public") isPublic.push(f);
  for (const f of isPublic) assert.ok(published.has(f), `${f} is marked public but no page publishes it`);
  assert.ok(isPublic.length >= 3);
});

test("every page carries a status, and a page of docs/ shows the one the manifest gives it", async () => {
  for (const f of await pages()) {
    const h = await readFile(join(out, f), "utf8");
    const m = h.match(/<p class="status status-([a-z-]+)" data-status="([a-z-]+)">/);
    assert.ok(m, `${f} has no status`);
    assert.ok(STATUSES[m[2]], `${f}: status ${m[2]}`);
  }
  const sdks = await readFile(join(out, "sdks.html"), "utf8");
  assert.match(sdks, /data-status="pre-release"/);
  assert.equal(pub.files["18-sdks.md"].status, "pre-release");
  assert.match(await readFile(join(out, "webhooks.html"), "utf8"), /data-status="stable"/);
  assert.match(await readFile(join(out, "authentication.html"), "utf8"), /data-status="draft"/);
});

// ---- the concept pages

test("the concept pages are built, in the sidebar, and say what the repository documents say", async () => {
  for (const page of ["authentication", "idempotency", "usage-metering", "test-mode", "errors", "webhooks"]) {
    const h = await readFile(join(out, `${page}.html`), "utf8");
    assert.match(h, new RegExp(`href="${page}\\.html"`), `${page} is not in the sidebar`);
  }
  const auth = await readFile(join(out, "authentication.html"), "utf8");
  for (const role of doc.components.schemas.ApiKeyRole.enum) assert.ok(auth.includes(`<code>${role}</code>`), `role ${role} is not explained`);
  const idem = await readFile(join(out, "idempotency.html"), "utf8");
  assert.ok(idem.includes("Idempotency-Key"));
  assert.match(await readFile(join(out, "test-mode.html"), "utf8"), /\/v1\/sandbox\/close-period/);
});

test("the pages for concepts do not point at a path the API does not have", async () => {
  const known = new Set(Object.keys(doc.paths));
  const toPattern = (p) => new RegExp(`^${p.replace(/\{[^}]+\}/g, "[^/]+")}$`);
  for (const page of ["authentication", "idempotency", "usage-metering", "test-mode", "errors", "getting-started", "cli", "tutorial-usage-billing", "tutorial-webhooks", "tutorial-payments"]) {
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    const seen = [];
    for (const m of md.matchAll(/`(?:GET |POST |PUT |PATCH |DELETE )?(\/(?:v1|app\/v1)\/[A-Za-z0-9_\-/{}.]+)`/g)) seen.push(m[1].replace(/\/$/, ""));
    // The tutorials call the API in code blocks: $BASE/v1/plans/starter/versions, $BASE/v1/invoices/$INVOICE_ID/links?x=1
    for (const m of md.matchAll(/\$BASE(\/v1\/[A-Za-z0-9_\-/$]+)/g)) seen.push(m[1].replace(/\/$/, "").split("/").map((seg) => (seg.startsWith("$") ? "{param}" : seg)).join("/"));
    for (const path of seen) {
      assert.ok([...known].some((k) => toPattern(k).test(path) || k === path), `${page}.md names ${path}, which is not in the OpenAPI file`);
    }
    if (["tutorial-usage-billing", "tutorial-payments"].includes(page)) assert.ok(seen.length >= 3, `${page}.md calls fewer than three API routes: is the check reading it?`);
  }
});

test("every method a tutorial calls is one the route has", async () => {
  // `api -X POST $BASE/v1/...` and `api $BASE/v1/... -d` (a body means POST) against the verbs of the OpenAPI file.
  let checked = 0;
  for (const page of ["tutorial-usage-billing", "tutorial-payments"]) {
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    for (const m of md.matchAll(/^api (?:-X (POST|GET|PUT|PATCH|DELETE) )?(?:"?\$BASE)(\/v1\/[A-Za-z0-9_\-/$]+)([^\n]*)$/gm)) {
      const path = m[2].replace(/\/$/, "").split("/").map((seg) => (seg.startsWith("$") ? "{param}" : seg)).join("/");
      const verb = (m[1] ?? (/ -d /.test(m[3]) ? "POST" : "GET")).toLowerCase();
      const route = Object.entries(doc.paths).find(([k]) => new RegExp(`^${k.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(path));
      assert.ok(route?.[1][verb], `${page}.md: ${verb.toUpperCase()} ${path} is not an operation of the OpenAPI file`);
      checked += 1;
    }
  }
  assert.ok(checked >= 10, `only ${checked} calls were read`);
});

// ---- the error catalogue

test("the error catalogue has every key of docs/error-keys.md except the operator's, each with a meaning", async () => {
  const keys = errorKeys(await readFile(join(docsDir, "error-keys.md"), "utf8"));
  assert.ok(keys.length > 300);
  const meanings = meaningsOf(JSON.parse(await readFile(resolve(here, "../web/admin/src/locales/en.json"), "utf8")));
  const html = await readFile(join(out, "errors.html"), "utf8");
  const without = [];
  for (const k of keys) {
    const operator = OPERATOR_AREAS.includes(k.split(".")[0]);
    assert.equal(html.includes(`id="key-${k}"`), !operator, `${k}: ${operator ? "is the operator's but is listed" : "is missing"}`);
    if (!operator && !meanings[k]) without.push(k);
  }
  assert.deepEqual(without, [], `these keys have no errors.<key> message in web/admin/src/locales/en.json: ${without.join(", ")}`);
  assert.ok(html.includes("Translate the key, not the message"));
});

test("every error_code of a rejected usage event is listed and explained", () => {
  const codes = doc.components.schemas.EventResult.properties.error_code.enum;
  assert.ok(codes.length >= 10);
  for (const c of codes) assert.ok(EVENT_CODES[c], `${c} has no meaning in EVENT_CODES`);
  for (const c of Object.keys(EVENT_CODES)) assert.ok(codes.includes(c), `${c} is explained but the API does not send it`);
});

// ---- language tabs

test("the operations that send usage and verify a webhook have a tab for every language, made by the console's generator", async () => {
  const ids = Object.keys(TABBED);
  assert.deepEqual(ids.sort(), ["createWebhookEndpoint", "ingestEvents"]);
  const events = await readFile(join(out, "api", "events.html"), "utf8");
  const hooks = await readFile(join(out, "api", "webhook-endpoints.html"), "utf8");
  for (const l of LANGS) {
    assert.ok(events.includes(`role="tabpanel" data-lang="${l}"`), `events: no ${l} tab`);
    assert.ok(hooks.includes(`role="tabpanel" data-lang="${l}"`), `webhook-endpoints: no ${l} tab`);
  }
  assert.ok(events.includes("payrudder.events.ingest("));
  assert.ok(events.includes("/v1/events"));
  assert.ok(hooks.includes("constructEvent"));
  // The install lines are the honest ones.
  assert.ok(events.includes("payrudder-sdk-0.0.0.tgz"));
  assert.ok(!events.includes("quotify"));
  // An operation without tabs keeps its curl.
  assert.ok((await readFile(join(out, "api", "meters.html"), "utf8")).includes("<summary>curl</summary>"));
});

// ---- the command line, the tutorials, the change log

test("the command line page documents every command the program lists", async () => {
  const go = await readFile(resolve(here, "../sdk/go/cmd/payrudder/cli.go"), "utf8");
  const usage = go.slice(go.indexOf("const usageText = `"), go.indexOf("`", go.indexOf("const usageText = `") + 20));
  const commands = [...usage.matchAll(/^ {2}([a-z]+(?: [a-z]+)?) {2,}\S/gm)].map((m) => m[1]);
  assert.ok(commands.length >= 10, `read only ${commands.length} commands from the usage text`);
  const html = await readFile(join(out, "cli.html"), "utf8");
  for (const c of commands) assert.ok(html.includes(`payrudder ${c}`), `cli.html does not show "payrudder ${c}"`);
  assert.match(html, /data-status="pre-release"/);
  const sdks = (await readFile(join(out, "sdks.html"), "utf8")).split("<main>")[1] ?? "";
  assert.match(sdks, /<a href="cli\.html">Command line<\/a>/, "the SDKs page should lead to the command line");
});

test("the three tutorials are built, in the sidebar, and link only to pages and anchors that exist", async () => {
  for (const page of ["tutorial-usage-billing", "tutorial-webhooks", "tutorial-payments"]) {
    const h = await readFile(join(out, `${page}.html`), "utf8");
    assert.match(h, /data-status="draft"/);
    assert.match(h, new RegExp(`href="${page}\\.html"`), `${page} is not in the sidebar`);
  }
  // The links to the API reference survive (they used to be turned into plain text).
  assert.match(await readFile(join(out, "tutorial-usage-billing.html"), "utf8"), /href="api\/plans\.html"/);
  // The webhook tutorial ends with the handler in every language, made by the console's generator.
  const hooks = await readFile(join(out, "tutorial-webhooks.html"), "utf8");
  for (const l of LANGS) assert.ok(hooks.includes(`role="tabpanel" data-lang="${l}"`), `no ${l} handler`);
  assert.ok(hooks.includes("constructEvent"));
});

test("the errors page maps every error type of the four SDKs and says what the command line does", async () => {
  const html = await readFile(join(out, "errors.html"), "utf8");
  const sdk = (f) => readFile(resolve(here, "../sdk", f), "utf8");
  const goKinds = [...(await sdk("go/errors.go")).matchAll(/^\t(Kind[A-Za-z]+)\b/gm)].map((m) => m[1]);
  assert.ok(goKinds.length >= 9);
  const ts = [...(await sdk("typescript/src/errors.ts")).matchAll(/^export class (\w+)/gm)].map((m) => m[1]);
  const py = [...(await sdk("python/src/payrudder/errors.py")).matchAll(/^class (\w+)/gm)].map((m) => m[1]).filter((n) => !["PayrudderError", "ConfigurationError", "WebhookSignatureError"].includes(n));
  const php = (await readdir(resolve(here, "../sdk/php/src/Exception"))).map((f) => f.replace(".php", "")).filter((n) => !["PayrudderException", "ConfigurationException", "UnexpectedResponseException", "WebhookException"].includes(n));
  for (const n of [...goKinds, ...ts, ...py, ...php]) assert.ok(html.includes(`<code>${n}</code>`), `errors.html does not map ${n}`);
  assert.match(html, /exits 1/);
});

test("a link to the API reference stays a link only when that page exists", async () => {
  const h = await readFile(join(out, "tutorial-payments.html"), "utf8");
  assert.match(h, /href="api\/customers\.html"/);
});
