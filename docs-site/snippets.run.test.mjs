// The quick-start snippets of the Connect page (and of the reference's language tabs) are RUN, not only compiled: curl, TypeScript, Python, Go and PHP, with the SDKs of this
// checkout, against docs-site/fakeapi.mjs, a stand-in made from api/openapi/v1.yaml. What this proves: the code a developer pastes starts, reaches the address it was
// given, sends a bearer key and a body that fits the contract (known route, required fields, formats), and reads the answer. What it does not prove: that the real gateway
// accepts the request (no database, no key check, no business rule) - that is the compose stack's job, which no job here starts. PHP runs through a small autoloader
// stand-in, because the SDK has no autoloader outside Composer.
//
// A tool that is missing skips its language, unless SNIPPETS_REQUIRE_TOOLS=1 (the snippets-run workflow sets it), where it fails. SNIPPETS_PYTHON names a Python that
// has httpx. The TypeScript SDK must be built first (cd sdk/typescript && npm ci && npm run build).
import assert from "node:assert/strict";
import { execFile, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, describe, test } from "node:test";
import { promisify } from "node:util";
import { snippets } from "../web/admin/src/features/connect/snippets.ts";
import { check, inRepo, loadSpec, startFakeApi } from "./fakeapi.mjs";

const run = promisify(execFile);
const required = process.env.SNIPPETS_REQUIRE_TOOLS === "1";
const doc = await loadSpec();
const work = await mkdtemp(join(tmpdir(), "snippets-run-"));
after(() => rm(work, { recursive: true, force: true }));

const works = (cmd, args) => spawnSync(cmd, args, { timeout: 30_000 }).status === 0;
/** A reason to skip, or fail when the tools are required. */
function missing(tool, ok) {
  if (ok) return false;
  if (required) throw new Error(`${tool} is required (SNIPPETS_REQUIRE_TOOLS=1) but not usable`);
  return `${tool} is not available`;
}

const sets = {
  typical: { meter: "api_calls", customer: "cus_42", keyHint: "qk_test_" },
  // A value that tries to get out of every kind of string: quotes of both kinds, a backslash, a dollar, a backtick, a newline.
  hostile: { meter: 'm"1\\x', customer: "x\"'; $(id) `id` \\ \nz", keyHint: "qk_live_" },
};
const folded = (s) => s.replace(/\r?\n/g, " "); // a newline in a customer id is folded to a space on purpose

/** Runs one snippet's `send` code and checks what reached the fake. `sdk` snippets make the customer first, curl only sends the event. */
async function runAndCheck(lang, set, start, { sdk }) {
  // curl and TypeScript read the key from PAYRUDDER_API_KEY; the Python and Go snippets show the key's shape ("qk_test_...") in the code, for the developer to replace.
  const key = (inputs) => (lang === "python" || lang === "go" || lang === "php" ? `${inputs.keyHint}...` : "qk_test_run");
  const api = await startFakeApi(doc);
  try {
    const dir = await mkdtemp(join(work, `${lang}-${set}-`));
    const inputs = { baseUrl: api.url, ...sets[set] };
    const out = await start(dir, snippets(lang, inputs).send, { ...process.env, PAYRUDDER_API_KEY: "qk_test_run", GOWORK: "off", GOTOOLCHAIN: "local" });
    assert.ok(/accepted/i.test(out), `the snippet did not print the result's status: ${out}`);
    assert.deepEqual(api.seen.map((r) => `${r.method} ${r.path}`), sdk ? ["POST /v1/customers", "POST /v1/events"] : ["POST /v1/events"]);
    for (const r of api.seen) {
      assert.deepEqual(r.problems, [], `${r.method} ${r.path}: ${r.problems.join("; ")}`);
      assert.equal(r.auth, `Bearer ${key(inputs)}`);
    }
    const event = api.seen.at(-1).body.events[0];
    assert.equal(event.customer_external_id, folded(inputs.customer));
    assert.equal(event.meter_code, folded(inputs.meter));
    if (sdk) assert.equal(api.seen[0].body.external_id, folded(inputs.customer));
  } finally {
    await api.close();
  }
}

describe("the fake API holds a request to the contract", () => {
  test("a body that lacks a required field, has a bad time or the wrong verb is refused and recorded", async () => {
    const api = await startFakeApi(doc);
    try {
      const post = (path, body) => fetch(api.url + path, { method: "POST", headers: { authorization: "Bearer k", "content-type": "application/json" }, body: JSON.stringify(body) });
      assert.equal((await post("/v1/events", { events: [{ customer_external_id: "c", meter_code: "m", quantity: "1", event_time: "2026-11-03T10:15:00Z" }] })).status, 202);
      assert.equal((await post("/v1/events", { events: [{ customer_external_id: "c", meter_code: "m", quantity: "1" }] })).status, 400);
      assert.equal((await post("/v1/events", { events: [{ customer_external_id: "c", meter_code: "m", quantity: "1", event_time: "yesterday" }] })).status, 400);
      assert.equal((await post("/v1/events", { events: [] })).status, 400);
      assert.equal((await fetch(`${api.url}/v1/events`, { headers: { authorization: "Bearer k" } })).status, 404);
      assert.equal((await fetch(`${api.url}/v1/events`, { method: "POST", body: "{}", headers: { "content-type": "application/json" } })).status, 400);
      assert.match(api.seen.at(-1).problems.join(), /bearer/);
    } finally {
      await api.close();
    }
  });

  test("the schema check reads references, enums, patterns and limits", () => {
    const customer = { $ref: "#/components/schemas/CreateCustomerRequest" };
    assert.deepEqual(check(doc, customer, { external_id: "a", name: "A", email: "a@b.co" }), []);
    assert.match(check(doc, customer, { external_id: "a", name: "A", email: "a@b.co", locale: "xx" }).join(), /locale/);
    assert.match(check(doc, customer, { external_id: "a", name: "A", email: "a@b.co", country: "tr" }).join(), /country/);
    assert.match(check(doc, customer, { external_id: "a".repeat(129), name: "A", email: "a@b.co" }).join(), /longer than 128/);
    assert.match(check(doc, customer, { name: "A", email: "a@b.co" }).join(), /external_id is required/);
  });
});

describe("the snippets run against it", () => {
  for (const set of Object.keys(sets)) {
    test(`curl (${set} values)`, { skip: missing("bash and curl", works("bash", ["-c", "command -v curl"])) }, () =>
      runAndCheck("curl", set, async (dir, code, env) => (await run("bash", ["-c", `set -e\n${code}`], { env, timeout: 30_000 })).stdout, { sdk: false }));

    test(`TypeScript (${set} values) with the SDK built from sdk/typescript`, { skip: missing("the built TypeScript SDK (cd sdk/typescript && npm ci && npm run build) and Node 22.18+", existsSync(inRepo("sdk/typescript/dist/index.js")) && Number(process.versions.node.split(".")[0]) >= 22) }, () =>
      runAndCheck("typescript", set, async (dir, code, env) => {
        // The page imports "@payrudder/sdk": a package of that name in node_modules, which is the checkout's SDK. .mts: an ES module (top-level await) whose types Node strips.
        await mkdir(join(dir, "node_modules/@payrudder"), { recursive: true });
        await symlink(inRepo("sdk/typescript"), join(dir, "node_modules/@payrudder/sdk"));
        await writeFile(join(dir, "send.mts"), code);
        return (await run(process.execPath, ["send.mts"], { cwd: dir, env, timeout: 60_000 })).stdout;
      }, { sdk: true }));

    const python = process.env.SNIPPETS_PYTHON ?? "python3";
    test(`Python (${set} values) with sdk/python`, { skip: missing("python3 with httpx (pip install httpx; or SNIPPETS_PYTHON)", works(python, ["-c", "import httpx"])) }, () =>
      runAndCheck("python", set, async (dir, code, env) => {
        await writeFile(join(dir, "send.py"), code);
        return (await run(python, ["send.py"], { cwd: dir, env: { ...env, PYTHONPATH: inRepo("sdk/python/src"), PYTHONDONTWRITEBYTECODE: "1" }, timeout: 60_000 })).stdout;
      }, { sdk: true }));

    test(`Go (${set} values) with sdk/go`, { skip: missing("go", works("go", ["version"])) }, () =>
      runAndCheck("go", set, async (dir, code, env) => {
        await writeFile(join(dir, "go.mod"), `module snippet\n\ngo 1.23\n\nrequire github.com/0furkancolak/payrudder/sdk/go v0.0.0\n\nreplace github.com/0furkancolak/payrudder/sdk/go => ${inRepo("sdk/go")}\n`);
        await writeFile(join(dir, "main.go"), code);
        return (await run("go", ["run", "."], { cwd: dir, env, timeout: 180_000 })).stdout;
      }, { sdk: true }));

    // The SDK has no autoloader outside Composer, so a four-line one stands in for it; the SDK's transport needs ext-curl.
    test(`PHP (${set} values) with sdk/php`, { skip: missing("php with ext-curl", works("php", ["-r", "exit(function_exists('curl_init') ? 0 : 1);"])) }, () =>
      runAndCheck("php", set, async (dir, code, env) => {
        const autoload = `spl_autoload_register(function ($c) { $f = ${JSON.stringify(inRepo("sdk/php/src"))} . '/' . str_replace('\\\\', '/', substr($c, 9)) . '.php'; if (str_starts_with($c, 'Payrudder\\\\') && is_file($f)) require $f; });`;
        await writeFile(join(dir, "send.php"), `<?php\n${autoload}\n${code}`);
        return (await run("php", ["send.php"], { cwd: dir, env, timeout: 60_000 })).stdout;
      }, { sdk: true }));
  }
});
