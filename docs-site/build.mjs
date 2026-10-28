// Builds the developer documentation as a static site: guides from Markdown, and the API reference from api/openapi/v1.yaml (the tenant API, /v1/*).
// node build.mjs [outDir]   (default: docs-site/dist)
import { mkdir, readFile, rm, writeFile, cp } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { marked } from "marked";
import { parse } from "yaml";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

/** The guides, in the order of the sidebar: file (relative to the repository or this folder), the page it becomes and its title. */
export const GUIDES = [
  { src: join(here, "content/getting-started.md"), page: "getting-started", title: "Getting started" },
  { src: join(root, "docs/16-webhooks.md"), page: "webhooks", title: "Webhooks" },
  { src: join(root, "docs/18-sdks.md"), page: "sdks", title: "SDKs" },
  { src: join(root, "docs/error-keys.md"), page: "error-keys", title: "Error keys" },
];

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const METHODS = ["get", "post", "put", "patch", "delete"];
const slug = (s) => String(s).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

// ---- the OpenAPI document

function resolveRef(doc, ref) {
  if (!ref.startsWith("#/")) throw new Error(`external reference ${ref}`);
  return ref.slice(2).split("/").reduce((n, k) => n?.[k.replace(/~1/g, "/").replace(/~0/g, "~")], doc);
}
const refName = (ref) => ref.split("/").pop();

/** The public operations: /v1/* with their path-level parameters, grouped by the first path segment. */
export function operationsOf(doc) {
  const out = [];
  for (const [path, item] of Object.entries(doc.paths ?? {})) {
    if (!path.startsWith("/v1/")) continue;
    for (const m of METHODS) {
      const op = item[m];
      if (!op) continue;
      const params = [...(item.parameters ?? []), ...(op.parameters ?? [])].map((p) => (p.$ref ? resolveRef(doc, p.$ref) : p));
      out.push({ method: m.toUpperCase(), path, id: op.operationId ?? `${m}-${slug(path)}`, group: path.split("/")[2] ?? "other", op, params });
    }
  }
  return out;
}

/** An example value of a schema: the first example/default/enum value, else something of the right type. Depth is bounded; a cycle ends as {}. */
export function exampleOf(doc, schema, depth = 0, seen = new Set()) {
  if (!schema) return null;
  if (schema.$ref) {
    if (seen.has(schema.$ref) || depth > 6) return {};
    return exampleOf(doc, resolveRef(doc, schema.$ref), depth + 1, new Set([...seen, schema.$ref]));
  }
  if (schema.example !== undefined) return schema.example;
  if (schema.examples?.length) return schema.examples[0];
  if (schema.default !== undefined) return schema.default;
  if (schema.enum?.length) return schema.enum[0];
  if (schema.oneOf?.length) return exampleOf(doc, schema.oneOf[0], depth + 1, seen);
  if (schema.allOf?.length) return Object.assign({}, ...schema.allOf.map((s) => exampleOf(doc, s, depth + 1, seen)));
  switch (schema.type) {
    case "object": {
      const o = {};
      const req = new Set(schema.required ?? []);
      for (const [k, v] of Object.entries(schema.properties ?? {})) if (req.has(k)) o[k] = exampleOf(doc, v, depth + 1, seen);
      return o;
    }
    case "array":
      return [exampleOf(doc, schema.items, depth + 1, seen)];
    case "integer":
    case "number":
      return schema.minimum ?? 1;
    case "boolean":
      return false;
    case "string":
      return schema.format === "date-time" ? "2026-11-03T10:15:00Z" : schema.format === "uuid" ? "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e" : "string";
    default:
      return null;
  }
}

function typeOf(doc, s) {
  if (!s) return "";
  if (s.$ref) return `<a href="#schema-${esc(refName(s.$ref))}">${esc(refName(s.$ref))}</a>`;
  if (s.enum) return s.enum.map((v) => `<code>${esc(v)}</code>`).join(" | ");
  if (s.oneOf) return s.oneOf.map((x) => typeOf(doc, x)).join(" | ");
  if (s.type === "array") return `array of ${typeOf(doc, s.items)}`;
  return esc([s.type, s.format].filter(Boolean).join(" · ")) || "any";
}

function fieldRows(doc, schema) {
  const s = schema?.$ref ? resolveRef(doc, schema.$ref) : schema;
  if (!s) return "";
  const req = new Set(s.required ?? []);
  const props = Object.entries(s.properties ?? {});
  if (props.length === 0) return "";
  const rows = props.map(([k, v]) => {
    const r = v.$ref ? resolveRef(doc, v.$ref) : v;
    return `<tr><td><code>${esc(k)}</code>${req.has(k) ? ' <span class="req">required</span>' : ""}</td><td>${typeOf(doc, v)}</td><td>${esc(v.description ?? r?.description ?? "")}</td></tr>`;
  });
  return `<table class="fields"><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>${rows.join("")}</tbody></table>`;
}

const jsonBlock = (v) => `<pre><code>${esc(JSON.stringify(v, null, 2))}</code></pre>`;

function curlOf(doc, o, base) {
  const body = o.op.requestBody?.content?.["application/json"]?.schema;
  const path = o.path.replace(/\{([^}]+)\}/g, (_, n) => `<${n}>`);
  const lines = [`curl -X ${o.method} ${base}${path} \\`, `  -H "Authorization: Bearer $PAYRUDDER_API_KEY"`];
  if (body) {
    lines[1] += " \\";
    lines.push(`  -H "Content-Type: application/json" \\`, `  -d '${JSON.stringify(exampleOf(doc, body)).replace(/'/g, "'\\''")}'`);
  }
  return lines.join("\n");
}

function operationHtml(doc, o, base) {
  const body = o.op.requestBody?.content?.["application/json"]?.schema;
  const params = o.params.filter((p) => p.in !== "header");
  const resps = Object.entries(o.op.responses ?? {}).map(([code, r]) => {
    const rr = r.$ref ? resolveRef(doc, r.$ref) : r;
    const sch = rr?.content?.["application/json"]?.schema;
    return `<tr><td><code>${esc(code)}</code></td><td>${esc(rr?.description ?? "")}</td><td>${sch ? typeOf(doc, sch) : ""}</td></tr>`;
  });
  const okResp = Object.entries(o.op.responses ?? {}).find(([c]) => c.startsWith("2"));
  const okSchema = okResp ? (okResp[1].$ref ? resolveRef(doc, okResp[1].$ref) : okResp[1])?.content?.["application/json"]?.schema : null;
  return `<section class="op" id="${esc(o.id)}">
<h3><span class="m m-${o.method.toLowerCase()}">${o.method}</span> <code>${esc(o.path)}</code> <a class="anchor" href="#${esc(o.id)}" aria-label="link">#</a></h3>
<p class="sum">${esc(o.op.summary ?? "")}</p>
${o.op.description ? `<div class="desc">${marked.parse(String(o.op.description))}</div>` : ""}
${params.length ? `<h4>Parameters</h4><table class="fields"><thead><tr><th>Name</th><th>In</th><th>Type</th><th>Description</th></tr></thead><tbody>${params.map((p) => `<tr><td><code>${esc(p.name)}</code>${p.required ? ' <span class="req">required</span>' : ""}</td><td>${esc(p.in)}</td><td>${typeOf(doc, p.schema)}</td><td>${esc(p.description ?? "")}</td></tr>`).join("")}</tbody></table>` : ""}
${body ? `<h4>Request body</h4>${fieldRows(doc, body)}<details><summary>Example</summary>${jsonBlock(exampleOf(doc, body))}</details>` : ""}
<h4>Responses</h4><table class="fields"><thead><tr><th>Status</th><th>Meaning</th><th>Body</th></tr></thead><tbody>${resps.join("")}</tbody></table>
${okSchema ? `<details><summary>Example response</summary>${jsonBlock(exampleOf(doc, okSchema))}</details>` : ""}
<details open><summary>curl</summary><pre><code>${esc(curlOf(doc, o, base))}</code></pre></details>
</section>`;
}

/** The schemas the operations mention, by name, so a `$ref` in a table has a place to land. */
function usedSchemas(doc, ops) {
  const names = new Set();
  const visit = (n, depth = 0) => {
    if (!n || typeof n !== "object" || depth > 12) return;
    if (n.$ref?.startsWith("#/components/schemas/")) {
      const name = refName(n.$ref);
      if (names.has(name)) return;
      names.add(name);
      visit(doc.components.schemas[name], depth + 1);
      return;
    }
    for (const v of Object.values(n)) visit(v, depth + 1);
  };
  for (const o of ops) {
    visit(o.op.requestBody);
    for (const r of Object.values(o.op.responses ?? {})) visit(r.$ref ? resolveRef(doc, r.$ref) : r);
    for (const p of o.params) visit(p);
  }
  return [...names].sort();
}

// ---- pages

const CSS = `:root{--bg:#fff;--fg:#17181c;--mut:#5d6270;--line:#e3e5ea;--soft:#f4f5f8;--brand:#5b3df5;--code:#f4f5f8}
@media(prefers-color-scheme:dark){:root{--bg:#121318;--fg:#e8e9ee;--mut:#9aa0ae;--line:#2a2d36;--soft:#1b1d25;--brand:#9d8bff;--code:#1b1d25}}
*{box-sizing:border-box}body{margin:0;font:16px/1.6 system-ui,-apple-system,Segoe UI,sans-serif;background:var(--bg);color:var(--fg)}
a{color:var(--brand)}code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.875em}
code{background:var(--code);padding:.1em .35em;border-radius:6px}pre{background:var(--code);padding:14px 16px;border-radius:12px;overflow-x:auto}pre code{background:none;padding:0}
.layout{display:grid;grid-template-columns:260px minmax(0,1fr);min-height:100vh}
nav.side{border-inline-end:1px solid var(--line);padding:20px 16px;position:sticky;top:0;height:100vh;overflow-y:auto}
nav.side h2{font-size:.75rem;text-transform:uppercase;letter-spacing:.06em;color:var(--mut);margin:20px 0 6px}
nav.side a{display:block;padding:3px 8px;border-radius:8px;text-decoration:none;color:var(--fg);font-size:.9rem}
nav.side a[aria-current=page]{background:var(--soft);font-weight:600}
main{padding:32px clamp(16px,5vw,64px);max-width:980px}
h1{font-size:2rem;margin-top:0}h3{margin-top:2.5rem}
.op{border-top:1px solid var(--line);padding-top:8px}.sum{font-weight:600;margin:.2rem 0}
.m{font-size:.7rem;font-weight:700;padding:2px 8px;border-radius:999px;color:#fff;vertical-align:middle}
.m-get{background:#2b7a4b}.m-post{background:#2f5fd0}.m-put{background:#b7791f}.m-patch{background:#8a4fbf}.m-delete{background:#c0392b}
table{border-collapse:collapse;width:100%;font-size:.9rem;margin:.5rem 0 1rem}th,td{text-align:start;padding:6px 10px;border-bottom:1px solid var(--line);vertical-align:top}
.req{color:#c0392b;font-size:.7rem}.anchor{text-decoration:none;opacity:.4;font-size:.9rem}
details{margin:.5rem 0}summary{cursor:pointer;color:var(--mut)}
#q{width:100%;padding:8px 10px;border:1px solid var(--line);border-radius:10px;background:var(--bg);color:var(--fg)}
#hits{list-style:none;padding:0;margin:6px 0}#hits a{font-size:.85rem}
@media(max-width:800px){.layout{grid-template-columns:1fr}nav.side{position:static;height:auto;border-inline-end:0;border-bottom:1px solid var(--line)}}`;

function layout({ title, body, nav, current, up = "" }) {
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>${esc(title)} · Payrudder developers</title><style>${CSS}</style></head>
<body><div class="layout">
<nav class="side" aria-label="Documentation"><a href="${up}index.html" style="font-weight:700">Payrudder developers</a>
<input id="q" type="search" placeholder="Search" aria-label="Search the documentation" autocomplete="off"><ul id="hits"></ul>
${nav.map((g) => `<h2>${esc(g.title)}</h2>${g.items.map((i) => `<a href="${up}${i.href}"${i.href === current ? ' aria-current="page"' : ""}>${esc(i.label)}</a>`).join("")}`).join("")}
</nav>
<main>${body}</main></div>
<script>(function(){var q=document.getElementById('q'),h=document.getElementById('hits'),idx=null;
function load(){return idx?Promise.resolve(idx):fetch('${up}search.json').then(function(r){return r.json()}).then(function(j){idx=j;return j})}
q.addEventListener('input',function(){var t=q.value.trim().toLowerCase();h.innerHTML='';if(t.length<2)return;load().then(function(j){
var words=t.split(/\\s+/),n=0;for(var i=0;i<j.length&&n<12;i++){var e=j[i],hay=(e.title+' '+e.text).toLowerCase();if(words.every(function(w){return hay.indexOf(w)>=0}))
{var li=document.createElement('li'),a=document.createElement('a');a.href='${up}'+e.url;a.textContent=e.title;li.appendChild(a);h.appendChild(li);n++}}})})})();</script>
</body></html>`;
}

const textOf = (html) => html.replace(/<[^>]+>/g, " ").replace(/&[a-z]+;/g, " ").replace(/\s+/g, " ").trim();

export async function build(outDir = join(here, "dist")) {
  const doc = parse(await readFile(join(root, "api/openapi/v1.yaml"), "utf8"));
  const base = "https://YOUR-API-ADDRESS";
  const ops = operationsOf(doc);
  const groups = [...new Set(ops.map((o) => o.group))].sort();

  const nav = [
    { title: "Guides", items: GUIDES.map((g) => ({ label: g.title, href: `${g.page}.html` })) },
    { title: "API reference", items: [{ label: "Overview", href: "api/index.html" }, ...groups.map((g) => ({ label: g, href: `api/${g}.html` }))] },
  ];
  await rm(outDir, { recursive: true, force: true });
  await mkdir(join(outDir, "api"), { recursive: true });
  const search = [];

  for (const g of GUIDES) {
    const md = await readFile(g.src, "utf8");
    // A link to another guide stays a link inside the site; a link to a file or folder of the repository (or a guide that is not published) is only its text,
    // because the repository is not part of the site.
    const html = marked.parse(md).replace(/<a href="([^"]*)"([^>]*)>([\s\S]*?)<\/a>/g, (m, href, rest, text) => {
      if (/^(https?:|mailto:|#)/.test(href)) return m;
      const [file, hash] = href.split("#");
      const name = file.split("/").pop() ?? "";
      const known = GUIDES.find((x) => x.src.endsWith(`/${name}`) || x.page === name.replace(/\.html$/, "").replace(/\.md$/, "").replace(/^\d+-/, ""));
      return known && !file.startsWith("..") ? `<a href="${known.page}.html${hash ? `#${hash}` : ""}"${rest}>${text}</a>` : text;
    });
    await writeFile(join(outDir, `${g.page}.html`), layout({ title: g.title, body: html, nav, current: `${g.page}.html` }));
    search.push({ title: g.title, url: `${g.page}.html`, text: textOf(html).slice(0, 4000) });
  }

  for (const g of groups) {
    const mine = ops.filter((o) => o.group === g);
    const schemas = usedSchemas(doc, mine);
    const body = `<h1>${esc(g)}</h1><p><a href="index.html">API reference</a></p>${mine.map((o) => operationHtml(doc, o, base)).join("")}
<h2>Models</h2>${schemas.map((n) => `<section id="schema-${esc(n)}"><h3>${esc(n)}</h3>${fieldRows(doc, { $ref: `#/components/schemas/${n}` }) || `<p>${typeOf(doc, doc.components.schemas[n])}</p>`}</section>`).join("")}`;
    await writeFile(join(outDir, "api", `${g}.html`), layout({ title: `${g} API`, body, nav, current: `api/${g}.html`, up: "../" }));
    for (const o of mine) search.push({ title: `${o.method} ${o.path}`, url: `api/${g}.html#${o.id}`, text: `${o.op.summary ?? ""} ${textOf(marked.parse(String(o.op.description ?? "")))}`.slice(0, 1500) });
  }

  const overview = `<h1>API reference</h1><p>${ops.length} operations of the tenant API, authenticated with an API key (<code>Authorization: Bearer qk_…</code>). The same OpenAPI file, <code>api/openapi/v1.yaml</code>, is what the SDKs are generated from.</p>
<ul>${groups.map((g) => `<li><a href="${g}.html">${esc(g)}</a> (${ops.filter((o) => o.group === g).length})</li>`).join("")}</ul>`;
  await writeFile(join(outDir, "api", "index.html"), layout({ title: "API reference", body: overview, nav, current: "api/index.html", up: "../" }));

  const home = `<h1>Payrudder developers</h1><p>Meter usage, price it, invoice it, collect the money. Start with <a href="getting-started.html">Getting started</a>, then use the <a href="api/index.html">API reference</a>.</p>
<ul>${GUIDES.map((g) => `<li><a href="${g.page}.html">${esc(g.title)}</a></li>`).join("")}<li><a href="api/index.html">API reference</a></li></ul>`;
  await writeFile(join(outDir, "index.html"), layout({ title: "Documentation", body: home, nav, current: "index.html" }));
  await writeFile(join(outDir, "search.json"), JSON.stringify(search));
  return { outDir, operations: ops.length, groups };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const r = await build(process.argv[2] ? resolve(process.argv[2]) : undefined);
  console.log(`docs: ${r.operations} operations in ${r.groups.length} groups -> ${r.outDir}`);
}
