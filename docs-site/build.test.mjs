import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, readdir, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { parse } from "yaml";
import { EVENT_CODES, GUIDES, OPERATOR_AREAS, REDIRECTS, STATUSES, TABBED, build, errorKeys, exampleOf, externalLinksOf, loadPublication, meaningsOf, operationsOf, publicationOf, versionLine } from "./build.mjs";
import { LANGS } from "../web/admin/src/features/connect/snippets.ts";

const here = dirname(fileURLToPath(import.meta.url));
const doc = parse(await readFile(resolve(here, "../api/openapi/v1.yaml"), "utf8"));
const out = await mkdtemp(join(tmpdir(), "docs-"));
const built = await build(out);

/** The pages of the site; the small pages that send an old address to its new one (REDIRECTS) are not pages. */
async function pages() {
  const all = [];
  for (const f of await readdir(out)) if (f.endsWith(".html") && !(f in REDIRECTS)) all.push(f);
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

test("the public text of the tenant API (the description of the API, of every /v1 operation and of every model they reach) names no operator route and no deployment variable", () => {
  const seen = new Set();
  const texts = [["info.description", doc.info.description]];
  const walk = (n, path) => {
    if (typeof n === "string") return void (path.endsWith("description") || path.endsWith("summary") ? texts.push([path, n]) : null);
    if (Array.isArray(n)) return n.forEach((x, i) => walk(x, `${path}[${i}]`));
    if (!n || typeof n !== "object") return;
    if (typeof n.$ref === "string" && n.$ref.startsWith("#/")) {
      if (seen.has(n.$ref)) return;
      seen.add(n.$ref);
      return walk(n.$ref.slice(2).split("/").reduce((x, k) => x?.[k], doc), n.$ref.slice(2));
    }
    for (const [k, v] of Object.entries(n)) walk(v, `${path}.${k}`);
  };
  for (const o of operationsOf(doc)) walk(o.op, `${o.method} ${o.path}`);
  assert.ok(texts.length > 500, `only ${texts.length} texts were read`);
  const bad = texts.filter(([, t]) => /\/admin\/|\/app\/v1|\/auth\/|PAYRUDDER_[A-Z_]+|payrudderctl/.test(t));
  assert.deepEqual(bad.map(([p, t]) => `${p}: ${t.match(/\/admin\/\S*|\/app\/v1\S*|\/auth\/\S*|PAYRUDDER_[A-Z_]+|payrudderctl/)[0]}`), [], "rewrite these descriptions for a reader outside the deployment (api/openapi/v1.yaml)");
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

/** The concepts that had no page of their own (plan 19): what a customer buys, what ties them to it, what they are billed, what they can spend, and what happens to a payment that fails. */
const CONCEPT_PAGES = ["plans-and-packages", "subscriptions", "invoices", "credits", "dunning"];

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
  for (const page of ["authentication", "idempotency", "usage-metering", "test-mode", "errors", "getting-started", "cli", "tutorial-usage-billing", "tutorial-webhooks", "tutorial-payments", "migrate-from-stripe", "migrate-from-paddle", "migrate-from-iyzico", "provider-stripe", "provider-paddle", "provider-iyzico", "tax-and-einvoices", "security", "console-customers", "console-invoices", "console-pricing", "console-webhooks", "console-go-live", "versioning", ...CONCEPT_PAGES]) {
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

test("every error_code of a rejected usage event is listed, explained and has a hint of what to do", async () => {
  const codes = doc.components.schemas.EventResult.properties.error_code.enum;
  assert.ok(codes.length >= 10);
  for (const c of codes) assert.ok(EVENT_CODES[c], `${c} has no meaning in EVENT_CODES`);
  for (const c of Object.keys(EVENT_CODES)) assert.ok(codes.includes(c), `${c} is explained but the API does not send it`);
  // The hint is the console's English text (connect.eventErrors.<code>), so the docs and the Connect page cannot say different things.
  const hints = JSON.parse(await readFile(resolve(here, "../web/admin/src/locales/en.json"), "utf8")).connect.eventErrors;
  const html = await readFile(join(out, "errors.html"), "utf8");
  for (const c of codes) {
    assert.ok(hints[c]?.length > 20, `${c} has no hint in connect.eventErrors of the English locale`);
    assert.ok(html.includes(`id="event-code-${c}"`) && html.includes(hints[c].replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch])), `${c}: the errors page does not show its hint`);
  }
  assert.deepEqual(Object.keys(hints).sort(), [...codes].sort(), "the English hints name a code the API does not send, or lack one it does");
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

// ---- search over models, renamed pages, links to other sites (plan 19)

test("the search finds a model by its name, by a field name and by what a field says, and every hit lands on an anchor that exists", async () => {
  const idx = JSON.parse(await readFile(join(out, "search.json"), "utf8"));
  const models = idx.filter((e) => e.kind === "Model");
  assert.ok(models.length > 100, `only ${models.length} models are searchable`);
  assert.ok(idx.some((e) => e.kind === "Guide") && idx.some((e) => e.kind === "Operation"));
  const create = models.find((e) => e.title === "CreateCustomerRequest");
  assert.ok(create, "CreateCustomerRequest is not in the index");
  assert.match(create.text, /\bexternal_id\b/);
  assert.match(create.text, /language of the customer's mails/);
  const ids = new Map();
  for (const e of idx) {
    const [file, frag] = e.url.split("#");
    if (!ids.has(file)) ids.set(file, new Set([...(await readFile(join(out, file), "utf8")).matchAll(/id="([^"]+)"/g)].map((m) => m[1])));
    if (frag) assert.ok(ids.get(file).has(frag), `${e.title} -> ${e.url} has no anchor`);
  }
});

test("a renamed page keeps its old address: a page that sends the browser on, to a page that exists", async () => {
  assert.deepEqual(Object.keys(REDIRECTS), ["error-keys.html"]);
  for (const [from, to] of Object.entries(REDIRECTS)) {
    assert.ok(!GUIDES.some((g) => `${g.page}.html` === from), `${from} is a page again: drop its redirect`);
    const stub = await readFile(join(out, from), "utf8");
    assert.ok(stub.includes(`http-equiv="refresh" content="0; url=${to}"`), `${from} does not refresh to ${to}`);
    assert.ok(stub.includes(`rel="canonical" href="${to}"`));
    assert.ok(stub.includes(`<a href="${to}">`), "a reader without scripts gets a link");
    assert.ok(existsSync(join(out, to)), `${from} points at ${to}, which is not built`);
  }
});

test("the links to other sites are https, are listed in external-links.txt, and (only with CHECK_EXTERNAL_LINKS=1) answer", async (t) => {
  const external = await externalLinksOf(out);
  assert.ok(external.size >= 2, "no link to another site was found: is the check reading the pages?");
  const listed = (await readFile(join(out, "external-links.txt"), "utf8")).trim().split("\n").map((l) => l.split("\t")[0]);
  assert.deepEqual(listed, [...external.keys()]);
  for (const [url, pagesWith] of external) {
    assert.ok(url.startsWith("https://"), `${[...pagesWith].join(", ")} links to ${url}: use https`);
    assert.doesNotThrow(() => new URL(url), url);
  }
  t.diagnostic(`${external.size} links to other sites: ${[...external.keys()].join(" ")}`);
  if (process.env.CHECK_EXTERNAL_LINKS !== "1") return t.skip("offline; set CHECK_EXTERNAL_LINKS=1 to ask each link");
  // Online and on request: a link that answers with an error status, or not at all, is listed together.
  const dead = [];
  for (const url of external.keys()) {
    try {
      const res = await fetch(url, { method: "GET", redirect: "follow", signal: AbortSignal.timeout(20_000) });
      if (res.status >= 400) dead.push(`${url} -> ${res.status}`);
    } catch (err) {
      dead.push(`${url} -> ${err.message}`);
    }
  }
  assert.deepEqual(dead, []);
});

// ---- the provider guides, the migration tutorial, the tax and security pages (plan 19)

test("the provider guides, the migration tutorial, and the tax and security pages are built, in the sidebar, draft, and link only to what exists", async () => {
  for (const page of ["migrate-from-stripe", "provider-stripe", "provider-paddle", "provider-iyzico", "tax-and-einvoices", "security"]) {
    const h = await readFile(join(out, `${page}.html`), "utf8");
    assert.match(h, /data-status="draft"/, page);
    assert.match(h, new RegExp(`href="${page}\\.html"`), `${page} is not in the sidebar`);
  }
  // The links between them stay links.
  const stripe = await readFile(join(out, "provider-stripe.html"), "utf8");
  assert.match(stripe, /href="tax-and-einvoices\.html"/);
  assert.match(await readFile(join(out, "migrate-from-stripe.html"), "utf8"), /href="provider-stripe\.html"/);
  assert.match(await readFile(join(out, "tax-and-einvoices.html"), "utf8"), /href="provider-paddle\.html"/);
});

test("a provider guide says what is not verified: no guide describes a provider as proven, and each names the matrix it comes from", async () => {
  for (const page of ["provider-stripe", "provider-paddle", "provider-iyzico"]) {
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    assert.match(md, /## What has not been verified/, `${page} has no section on what is unverified`);
    assert.match(md, /UNVERIFIED/, `${page} marks nothing UNVERIFIED`);
    assert.match(md, /compatibility matrix/, `${page} does not say where it comes from`);
    assert.match(md, /Nothing here has run against|No (iyzico|Paddle) account/, `${page} does not say it was never run`);
  }
});

test("what the security page says about the audit log, keys and webhook signatures matches the repository's documents", async () => {
  const md = await readFile(join(here, "content", "security.md"), "utf8");
  const audit = await readFile(resolve(here, "../docs/audit-log-and-two-factor.md"), "utf8");
  // Every area the page lists as recorded is an action the audit document records, and every area it lists as not recorded yet is still listed there as open.
  for (const action of ["api_key.", "member.", "plan.", "webhook_endpoint.", "customer.blocked", "credit_note.", "provider_account.", "einvoice_account.", "tenant.status_changed", "two_factor."]) {
    assert.ok(audit.includes(action), `the audit document no longer records ${action}: update security.md`);
  }
  assert.match(audit, /Not recorded yet/);
  assert.match(md, /GET \/v1\/audit-events/);
  assert.ok(doc.paths["/v1/audit-events"] && doc.paths["/v1/audit-events/export"]);
  const hooks = await readFile(resolve(here, "../docs/16-webhooks.md"), "utf8");
  assert.match(hooks, /Payrudder-Signature: t=/);
  assert.match(hooks, /24 hours/);
  assert.match(md, /Payrudder-Signature: t=/);
  assert.match(md, /24 hours/);
  assert.match(md, /five minutes/);
});

test("the tax page's API facts are the OpenAPI file's: the tax-rate route, the customer tax route and the e-document routes", () => {
  assert.ok(doc.paths["/v1/tax-rates"].put && doc.paths["/v1/customers/{external_id}/tax"].put && doc.paths["/v1/e-invoices/{id}/files/{kind}"].get);
  const warnings = doc.components.schemas.Customer.properties.tax_id_warning.description;
  for (const w of ["tr_individual", "tr_business", "tr_any", "tr_is_tckn", "tr_is_vkn", "eu_vat"]) assert.ok(warnings.includes(w), `${w} is not a tax_id_warning`);
});

// ---- the console guide, the migration tutorials, the versioning page (plan 19)

const CONSOLE_PAGES = ["console-customers", "console-invoices", "console-pricing", "console-webhooks", "console-go-live"];
const en = JSON.parse(await readFile(resolve(here, "../web/admin/src/locales/en.json"), "utf8"));
/** Every string of the English console, with `{placeholder}`s and a trailing ` ({placeholder})` taken out as well, so a guide can quote the part a reader sees. */
function consoleWords() {
  const all = new Set();
  const walk = (n) => {
    if (typeof n === "string") {
      all.add(n);
      all.add(n.replace(/ ?\(\{[a-zA-Z]+\}\)/g, "").replace(/ ?\{[a-zA-Z]+\}/g, "").trim());
    } else if (n && typeof n === "object") Object.values(n).forEach(walk);
  };
  walk(en);
  return all;
}

test("the console guide is built, in its own sidebar section, draft, and every name it puts in bold is a string of the English console", async () => {
  const words = consoleWords();
  const long = [...words].filter((w) => w.length >= 20);
  for (const page of CONSOLE_PAGES) {
    const h = await readFile(join(out, `${page}.html`), "utf8");
    assert.match(h, /data-status="draft"/, page);
    assert.match(h, new RegExp(`href="${page}\\.html"`), `${page} is not in the sidebar`);
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    const bold = [...md.matchAll(/\*\*([^*]+)\*\*/g)].map((m) => m[1]);
    assert.ok(bold.length >= 10, `${page}: only ${bold.length} names in bold: is the check reading it?`);
    // A name is a whole console string (or the part of it before a placeholder); a longer quotation may be part of one. Bold is for the console's words only.
    const unknown = bold.filter((b) => {
      const t = b.replace(/[.:]$/, "");
      return !words.has(b) && !words.has(t) && !(t.length >= 20 && long.some((w) => w.includes(t)));
    });
    assert.deepEqual(unknown, [], `${page}.md quotes the console with words it does not have (renamed in web/admin/src/locales/en.json?): ${unknown.join(" | ")}`);
  }
  assert.match(await readFile(join(out, "console-customers.html"), "utf8"), /href="console-invoices\.html"/, "the guides link to each other");
  assert.match(await readFile(join(out, "getting-started.html"), "utf8"), /href="console-go-live\.html"/);
});

test("the console guide only names sidebar items and sections that the console has", async () => {
  const sidebar = new Set([...Object.values(en.shell.items), ...Object.values(en.shell.sections)]);
  for (const name of ["Customers", "Customers and billing", "Invoices", "Payments", "Payment links", "Meters", "Packages", "Tax and retries", "Coupons", "Webhooks", "Go live", "Developers", "Usage and products", "Connect your app", "Sandbox tools"]) {
    assert.ok(sidebar.has(name), `"${name}" is not a sidebar item or section of the console any more`);
  }
});

test("the two migration tutorials are built, draft, say there is no importer, name the matrix they come from and mark what is UNVERIFIED", async () => {
  for (const [page, provider] of [["migrate-from-paddle", "Paddle"], ["migrate-from-iyzico", "iyzico"]]) {
    const h = await readFile(join(out, `${page}.html`), "utf8");
    assert.match(h, /data-status="draft"/, page);
    assert.match(h, new RegExp(`href="${page}\\.html"`), `${page} is not in the sidebar`);
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    assert.match(md, /\*\*There is no importer\*\*/, page);
    assert.match(md, new RegExp(`no ${provider} account of any kind was used`), page);
    assert.match(md, /compatibility matrix/, page);
    assert.ok((md.match(/UNVERIFIED/g) ?? []).length >= 3, `${page} marks fewer than three points UNVERIFIED`);
    assert.match(md, /## What does not carry over/, page);
    assert.match(h, /href="provider-/, `${page} should link to the provider guide`);
  }
  // What the tutorials say about the API: a subscription cannot be attached to one the provider already runs, and the fields exist that they tell the reader to fill in.
  const create = doc.components.schemas.CreateSubscriptionRequest.properties;
  assert.ok(create.provider && !Object.keys(create).some((k) => /provider_(ref|subscription)|external_subscription/.test(k)), "the tutorials say a provider's subscription cannot be attached: the API now has a field for it");
  assert.ok(doc.paths["/v1/customers/{external_id}/payment-method"].put && doc.paths["/v1/credit-notes"].get && !doc.paths["/v1/credit-notes"].post && doc.paths["/v1/sandbox/close-period"].post);
  // The matrices are what the provider pages and the tutorials were read from: a tutorial that quotes a fact the matrix dropped must be re-read.
  const paddle = await readFile(resolve(here, "../docs/paddle-compatibility-matrix.md"), "utf8");
  const iyzico = await readFile(resolve(here, "../docs/iyzico-compatibility-matrix.md"), "utf8");
  for (const fact of ["payrudder_subscription_id", "/adjustments", "/discounts", "customer_already_exists", "meter.created"]) assert.ok(paddle.includes(fact), `the Paddle matrix no longer mentions ${fact}: re-read migrate-from-paddle.md`);
  for (const fact of ["cardUserKey:cardToken", "not returned to you", "enabled_installments", "10-15 s", "X-IYZ-SIGNATURE-V3"]) assert.ok(iyzico.includes(fact), `the iyzico matrix no longer mentions ${fact}: re-read migrate-from-iyzico.md`);
});

test("every verb and path the new pages name together is an operation of the OpenAPI file", async () => {
  // `POST /v1/plans/{code}/versions` in backticks: the route must have that verb (the path alone is checked above).
  let checked = 0;
  for (const page of [...CONSOLE_PAGES, "migrate-from-paddle", "migrate-from-iyzico", "versioning", ...CONCEPT_PAGES]) {
    const md = await readFile(join(here, "content", `${page}.md`), "utf8");
    for (const m of md.matchAll(/`(GET|POST|PUT|PATCH|DELETE) (\/v1\/[A-Za-z0-9_\-/{}.]+)`/g)) {
      const route = Object.entries(doc.paths).find(([k]) => new RegExp(`^${k.replace(/\{[^}]+\}/g, "[^/]+")}$`).test(m[2]));
      assert.ok(route?.[1][m[1].toLowerCase()], `${page}.md: ${m[1]} ${m[2]} is not an operation of the OpenAPI file`);
      checked += 1;
    }
  }
  assert.ok(checked >= 10, `only ${checked} verb and path pairs were read`);
});

test("the versioning page says what the repository does: one path version, nothing deprecated, pinned provider versions, nothing released", async () => {
  const h = await readFile(join(out, "versioning.html"), "utf8");
  assert.match(h, /data-status="draft"/);
  assert.match(h, /href="versioning\.html"/, "versioning is not in the sidebar");
  assert.equal(doc.info.version, "v1");
  assert.ok(Object.keys(doc.paths).filter((p) => p.startsWith("/v1/")).every((p) => p.startsWith("/v1/")));
  assert.ok(!(await readFile(resolve(here, "../api/openapi/v1.yaml"), "utf8")).includes("deprecated: true"), "an operation is deprecated now: versioning.md says none is");
  const stripe = await readFile(resolve(here, "../pkg/payments/stripe/stripe.go"), "utf8");
  const pin = stripe.match(/const APIVersion = "([^"]+)"/)?.[1];
  assert.ok(pin && h.includes(pin), `the page does not name the Stripe version the adapter pins (${pin})`);
  assert.match(await readFile(resolve(here, "../pkg/payments/paddle/paddle.go"), "utf8"), /Set\("Paddle-Version", "1"\)/);
  // Nothing is released: every SDK says 0.0.0 (scripts/sdk-release.mjs keeps them equal and refuses to publish it).
  assert.match(await readFile(resolve(here, "../sdk/go/client.go"), "utf8"), /const Version = "0\.0\.0"/);
  assert.match(await readFile(resolve(here, "../sdk/typescript/src/version.ts"), "utf8"), /VERSION = "0\.0\.0"/);
  const plans = await readFile(resolve(here, "../docs/03-services/plans-svc.md"), "utf8");
  assert.match(plans, /default 30, at least 14, at most 365/, "the price-change notice limits changed: update versioning.md");
  assert.ok(doc.paths["/v1/plans/{code}/versions/{version}"] && doc.paths["/v1/plans/{code}/price-changes"].post);
  assert.match(await readFile(resolve(here, "../buf.yaml"), "utf8"), /WIRE_JSON/);
});

// ---- the concept pages for packages, subscriptions, invoices, credits and dunning (plan 19)

test("the five concept pages are built, draft, in the Concepts section of the sidebar, and each value of an enum they explain is one the API has", async () => {
  const schemas = doc.components.schemas;
  const page = async (p) => readFile(join(out, `${p}.html`), "utf8");
  for (const p of CONCEPT_PAGES) {
    const h = await page(p);
    assert.match(h, /data-status="draft"/, p);
    assert.match(h, new RegExp(`<a href="${p}\\.html"`), `${p} is not in the sidebar`);
  }
  // Every value of the enums a page explains is on the page (a value added to the API fails here until the page explains it), and the page names no value the enum lacks.
  const explains = [
    ["subscriptions", schemas.Subscription.properties.state.enum],
    ["subscriptions", schemas.Subscription.properties.interval.enum],
    ["subscriptions", schemas.Subscription.properties.fee_timing.enum],
    ["invoices", schemas.Invoice.properties.kind.enum],
    ["invoices", schemas.Invoice.properties.status.enum],
    ["invoices", schemas.InvoiceLine.properties.kind.enum],
    ["credits", schemas.CreditLot.properties.source.enum],
    ["credits", schemas.CreditLedgerEntry.properties.kind.enum],
  ];
  for (const [p, values] of explains) {
    const h = await page(p);
    for (const v of values) assert.ok(h.includes(`<code>${v}</code>`) || h.includes(`${v}</code>`), `${p}.html does not explain ${v}`);
  }
  const states = new Set(schemas.Subscription.properties.state.enum);
  for (const m of (await page("subscriptions")).matchAll(/SUBSCRIPTION_STATE_[A-Z_]+/g)) assert.ok(states.has(m[0]), `subscriptions names ${m[0]}, which is not a state`);
  const lineKinds = new Set(schemas.InvoiceLine.properties.kind.enum);
  for (const m of (await readFile(join(here, "content", "invoices.md"), "utf8")).matchAll(/^\| `([A-Z_]+)` \|/gm)) assert.ok(lineKinds.has(m[1]), `invoices names the line kind ${m[1]}, which the API does not have`);
});

test("the webhook events the concept pages name are events the webhooks document lists", async () => {
  const events = await readFile(resolve(here, "../docs/16-webhooks.md"), "utf8");
  let named = 0;
  for (const p of CONCEPT_PAGES) {
    const md = await readFile(join(here, "content", `${p}.md`), "utf8");
    // `invoice.paid`, `credits.low`, `.changed` after another event (`subscription.created`, `.changed`) are all read; a bare `.x` takes the family of the event before it.
    let family = "";
    for (const m of md.matchAll(/`((?:subscription|invoice|credit_note|credits|payment|payment_link)\.[a-z_.]+|\.[a-z_]+)`/g)) {
      const name = m[1].startsWith(".") ? `${family}${m[1]}` : m[1];
      if (!m[1].startsWith(".")) family = m[1].split(".")[0];
      assert.ok(events.includes(`\`${name}\``), `${p}.md names the event ${name}, which docs/16-webhooks.md does not list`);
      named += 1;
    }
  }
  assert.ok(named >= 10, `only ${named} events were read: is the check reading the pages?`);
});

// ---- search over the guides, the version the site describes, and the pages that must keep working (plan 19)

test("the search finds a word anywhere in a guide, in the section it is in, and every section of every guide is findable and has an anchor", async () => {
  const idx = JSON.parse(await readFile(join(out, "search.json"), "utf8"));
  // `Meter versions` is far below the first 4000 characters of the versioning page, which is all the search used to read of a guide.
  const meter = idx.find((e) => e.title === "Versioning: Meter versions");
  assert.ok(meter, "the section is not in the index");
  assert.equal(meter.url, "versioning.html#meter-versions");
  assert.match(meter.text, /decimal places/);
  const versioning = await readFile(join(out, "versioning.html"), "utf8");
  assert.ok(versioning.indexOf('id="meter-versions"') > 4000, "the section is no longer deep in the page: pick another example");
  assert.match(versioning, /<h2 id="meter-versions">Meter versions<\/h2>/);
  // Every h2 of every guide has an id and an entry that lands on it; an id appears once per page.
  let sections = 0;
  for (const g of GUIDES) {
    const h = await readFile(join(out, `${g.page}.html`), "utf8");
    const ids = [...h.slice(h.indexOf("<main>")).matchAll(/ id="([^"]+)"/g)].map((m) => m[1]);
    assert.equal(new Set(ids).size, ids.length, `${g.page}.html has an id twice`);
    for (const m of h.slice(h.indexOf("<main>")).matchAll(/<h2([^>]*)>([\s\S]*?)<\/h2>/g)) {
      const id = /id="([^"]+)"/.exec(m[1])?.[1];
      assert.ok(id, `${g.page}.html: the section "${m[2]}" has no id`);
      assert.ok(idx.some((e) => e.url === `${g.page}.html#${id}` && e.kind === "Guide"), `${g.page}.html#${id} is not in the search index`);
      sections += 1;
    }
  }
  assert.ok(sections > 100, `only ${sections} sections were read`);
  // A repeated heading gets its own id, and the entities of a heading are not left in its title.
  assert.ok(idx.every((e) => !/&#\d+;|&amp;|&quot;/.test(e.title)), "a title shows an entity");
});

test("every page says which version of the API it describes, the same as the OpenAPI file, and the commit it was built from when git is there", async () => {
  assert.equal(doc.info.version, "v1");
  for (const f of ["index.html", "versioning.html", "api/events.html"]) {
    const h = await readFile(join(out, f), "utf8");
    const line = /data-api-version>([^<]*)</.exec(h)?.[1] ?? "";
    assert.match(line, /^API v1\b/, f);
  }
  assert.equal(versionLine("v9", resolve(here, "..")).startsWith("API v9"), true);
  assert.equal(versionLine("v9", tmpdir()), "API v9", "a folder that is not a git checkout leaves the commit out");
  const page = await readFile(join(here, "content", "versioning.md"), "utf8");
  assert.match(page, /sidebar names the API version/);
});

test("every address the site has had still works: a page, or a redirect to a page, and a new page is recorded in pages.txt", async (t) => {
  const file = join(here, "pages.txt");
  const now = (await pages()).sort();
  if (process.env.UPDATE_PAGES === "1") {
    const keep = (await readFile(file, "utf8")).split("\n").filter(Boolean);
    await writeFile(file, [...new Set([...keep, ...now])].sort().join("\n") + "\n");
    return t.diagnostic("pages.txt rewritten: it keeps every address it ever listed");
  }
  const listed = (await readFile(file, "utf8")).split("\n").filter(Boolean);
  const missing = now.filter((f) => !listed.includes(f));
  assert.deepEqual(missing, [], "a new page: record its address with UPDATE_PAGES=1 node --test docs-site/build.test.mjs");
  for (const f of listed) {
    if (now.includes(f)) continue;
    assert.ok(f in REDIRECTS, `${f} was a page and is gone: add it to REDIRECTS in build.mjs (the address may be linked from elsewhere), or take it off pages.txt if it was never published`);
    assert.ok(existsSync(join(out, f)) && existsSync(join(out, dirname(f), REDIRECTS[f])), `${f} redirects to ${REDIRECTS[f]}, which is not built`);
  }
});
