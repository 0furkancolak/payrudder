import assert from "node:assert/strict";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import vm from "node:vm";
import { build } from "./build.mjs";
import { clientScript } from "./client.mjs";

// The script every page carries, run against a small stand-in for the DOM: the language tabs and the search ranking.

/** An element with the few things the script touches. */
function el(attrs = {}) {
  const listeners = {};
  return {
    attrs: { ...attrs },
    hidden: false,
    getAttribute(k) { return this.attrs[k] ?? null; },
    setAttribute(k, v) { this.attrs[k] = v; },
    addEventListener(type, fn) { (listeners[type] ??= []).push(fn); },
    click() { (listeners.click ?? []).forEach((f) => f()); },
  };
}
/** A group of code in the given languages, as build.mjs writes it. */
function group(langs) {
  const tabs = langs.map((l) => el({ role: "tab", "data-lang": l, "aria-selected": "false" }));
  const panels = langs.map((l) => el({ role: "tabpanel", "data-lang": l }));
  return { tabs, panels, querySelectorAll: (sel) => (sel === "[role=tab]" ? tabs : sel === "[role=tabpanel]" ? panels : []) };
}
const shown = (g) => g.tabs.filter((t) => t.attrs["aria-selected"] === "true").map((t) => t.attrs["data-lang"]);
const visible = (g) => g.panels.filter((p) => !p.hidden).map((p) => p.attrs["data-lang"]);

/** Runs the script on a page with these groups and this storage ("throws" is a browser that refuses storage). */
function page(groups, storage) {
  const q = el();
  q.value = "";
  const window = {};
  const localStorage = storage === "throws" ? { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } } : { getItem: (k) => storage.get(k) ?? null, setItem: (k, v) => storage.set(k, v) };
  const document = {
    getElementById: (id) => (id === "q" ? q : el()),
    querySelectorAll: (sel) => (sel === ".tabs" ? groups : []),
    createElement: () => el(),
  };
  vm.runInNewContext(clientScript(""), { document, window, localStorage, fetch: async () => ({ json: async () => [] }) });
  // Arrays made inside the script's context are not this context's Arrays: copy them.
  return { rank: (index, query) => Array.from(window.payrudderDocs.rank(index, query)) };
}

test("the language chosen in one group is shown in every group of the page, and is remembered", () => {
  const storage = new Map();
  const a = group(["curl", "typescript", "python"]);
  const b = group(["curl", "go"]);
  page([a, b], storage);
  // Nothing chosen yet: the first of each group.
  assert.deepEqual([shown(a), shown(b)], [["curl"], ["curl"]]);
  assert.deepEqual(visible(a), ["curl"]);
  a.tabs[2].click();
  assert.deepEqual([shown(a), visible(a)], [["python"], ["python"]]);
  // b has no Python tab: it keeps its first rather than showing nothing.
  assert.deepEqual([shown(b), visible(b)], [["curl"], ["curl"]]);
  assert.equal(storage.get("payrudder-docs-lang"), "python");
  b.tabs[1].click();
  assert.deepEqual([shown(a), shown(b)], [["curl"], ["go"]]);

  // Another page of the site, later: the choice is still there.
  const c = group(["curl", "typescript", "go"]);
  page([c], storage);
  assert.deepEqual(shown(c), ["go"]);
});

test("a browser that refuses storage still switches tabs", () => {
  const a = group(["curl", "php"]);
  assert.doesNotThrow(() => page([a], "throws"));
  a.tabs[1].click();
  assert.deepEqual([shown(a), visible(a)], [["php"], ["php"]]);
});

test("a page without code has no tabs to set up", () => {
  assert.doesNotThrow(() => page([], new Map()));
});

const idx = [
  { title: "Getting started", kind: "Guide", url: "getting-started.html", text: "create a customer and send a first usage event" },
  { title: "POST /v1/customers", kind: "Operation", url: "api/customers.html#createCustomer", text: "Create a customer" },
  { title: "Customer", kind: "Model", url: "api/customers.html#schema-Customer", text: "external_id name email" },
  { title: "CreateCustomerRequest", kind: "Model", url: "api/customers.html#schema-CreateCustomerRequest", text: "external_id name email locale" },
  { title: "Meter", kind: "Model", url: "api/meters.html#schema-Meter", text: "code aggregation customer property" },
];

test("search: every word must match; a title that has them all comes first, an exact title before that", () => {
  const { rank } = page([], new Map());
  assert.deepEqual(rank(idx, "customer").map((e) => e.title), ["Customer", "POST /v1/customers", "CreateCustomerRequest", "Getting started", "Meter"]);
  assert.deepEqual(rank(idx, "customer request").map((e) => e.title), ["CreateCustomerRequest"]);
  // A field name finds the models that carry it. The text counts when the title lacks the word: "customer" lists the guide and the meter after the titles that have it.
  assert.deepEqual(rank(idx, "external_id").map((e) => e.title), ["Customer", "CreateCustomerRequest"]);
    assert.deepEqual(rank(idx, "x"), [], "one letter searches nothing");
  assert.deepEqual(rank(idx, "nothing like this"), []);
  const many = Array.from({ length: 30 }, (_, i) => ({ title: `Customer ${i}`, kind: "Model", url: "x", text: "" }));
  assert.equal(rank(many, "customer").length, 12);
});

test("search: the built index finds the model for a field", async () => {
  const out = await mkdtemp(join(tmpdir(), "docs-client-"));
  await build(out);
  const index = JSON.parse(await readFile(join(out, "search.json"), "utf8"));
  const { rank } = page([], new Map());
  assert.equal(rank(index, "CreateCustomerRequest")[0].title, "CreateCustomerRequest");
  assert.ok(rank(index, "referral_touch_time").some((e) => e.kind === "Model" && e.title === "CreateCustomerRequest"));
});
