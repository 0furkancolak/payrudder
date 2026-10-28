import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, readdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { parse } from "yaml";
import { build, exampleOf, operationsOf } from "./build.mjs";

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
