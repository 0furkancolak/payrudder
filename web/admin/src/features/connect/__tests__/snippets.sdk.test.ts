import { execFile, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, describe, expect, it, vi } from "vitest";
import { LANGS, snippets, type Inputs, type Lang } from "../snippets";

/**
 * The snippets of the Connect page against the SDKs in this checkout, with no running stack: TypeScript is type-checked with tsc, Python is compiled and its SDK calls
 * are looked up on the real client, Go is vetted in a temporary module that replaces the SDK with the local one, PHP is linted and its classes and methods are looked up
 * in the SDK's source, and the curl command is run against a small local server that answers like the API. A snippet that names a method the SDK does not have, or an
 * install line that points nowhere, fails here instead of in a developer's first ten minutes.
 *
 * A tool that is not installed skips its language, unless SNIPPETS_REQUIRE_TOOLS=1 (the snippets workflow sets it), where it fails. SNIPPETS_PYTHON names a Python that
 * has httpx; SNIPPETS_PHP=docker runs PHP in php:8.4-cli like `make sdk-php-check`.
 */
// A cold `go vet` or the first tsc run takes far longer than vitest's default five seconds.
vi.setConfig({ testTimeout: 180_000 });
const repo = fileURLToPath(new URL("../../../../../../", import.meta.url));
const adminModules = join(repo, "web/admin/node_modules");
const required = process.env.SNIPPETS_REQUIRE_TOOLS === "1";
const work = mkdtempSync(join(tmpdir(), "snippets-"));
afterAll(() => rmSync(work, { recursive: true, force: true }));

const run = (cmd: string, args: string[], opts: { cwd?: string; env?: Record<string, string>; timeout?: number } = {}) =>
  spawnSync(cmd, args, { cwd: opts.cwd, encoding: "utf8", timeout: opts.timeout ?? 120_000, env: { ...process.env, GOWORK: "off", GOTOOLCHAIN: "local", ...opts.env } });
const works = (cmd: string, args: string[], env?: Record<string, string>) => run(cmd, args, { timeout: 30_000, ...(env ? { env } : {}) }).status === 0;
const out = (r: ReturnType<typeof run>) => `${r.stdout}${r.stderr}${r.error ?? ""}`;

const normal: Inputs = { baseUrl: "https://api.example.com", meter: "api_calls", customer: "cus_42", keyHint: "qk_test_" };
// A value that tries to get out of every kind of string: quotes of both kinds, a backslash, a dollar, a newline.
const hostile: Inputs = { baseUrl: "https://api.example.com/", meter: 'm"1\\x', customer: "x\"'; $(id) `id` \\ \nz", keyHint: "qk_live_" };

function gate(tool: string, ok: boolean) {
  if (!ok && required) throw new Error(`${tool} is required (SNIPPETS_REQUIRE_TOOLS=1) but not usable`);
  return ok;
}
function file(dir: string, name: string, text: string) {
  mkdirSync(join(dir, name, ".."), { recursive: true });
  writeFileSync(join(dir, name), text);
}
const checks = (name: string) => fileURLToPath(new URL(`./sdk-check/${name}`, import.meta.url));
const dirFor = (lang: Lang, set: string) => mkdtempSync(join(work, `${lang}-${set}-`));

describe.each([
  ["typical", normal],
  ["hostile", hostile],
])("snippets compile against the local SDKs (%s values)", (set, inputs) => {
  const typescript = gate("node_modules of the console (typescript)", works(process.execPath, [join(adminModules, "typescript/bin/tsc"), "-v"]));
  it.skipIf(!typescript)("TypeScript: send and webhook type-check against sdk/typescript/src", () => {
    const dir = dirFor("typescript", set);
    const s = snippets("typescript", inputs);
    // The webhook snippet is a handler of a web framework whose request and response types it does not import, so its two parameters are `any`.
    file(dir, "send.ts", s.send);
    file(dir, "webhook.ts", s.webhook);
    const config = (files: string[], strictAny: boolean) => ({
      compilerOptions: {
        target: "ES2022", module: "ESNext", moduleResolution: "Bundler", strict: true, noImplicitAny: strictAny, noEmit: true, skipLibCheck: true, types: ["node"],
        typeRoots: [join(adminModules, "@types")], baseUrl: ".", paths: { "@payrudder/sdk": [join(repo, "sdk/typescript/src/index.ts")] },
      },
      files,
    });
    for (const [name, strictAny] of [["send.ts", true], ["webhook.ts", false]] as const) {
      file(dir, `tsconfig.${name}.json`, JSON.stringify(config([name], strictAny)));
      const r = run(process.execPath, [join(adminModules, "typescript/bin/tsc"), "-p", join(dir, `tsconfig.${name}.json`)], { cwd: dir });
      expect(out(r), name).toBe("");
    }
  });

  const python = process.env.SNIPPETS_PYTHON ?? "python3";
  const pythonOk = gate("python3 with httpx (pip install httpx; or SNIPPETS_PYTHON)", works(python, ["-c", "import httpx"]));
  it.skipIf(!pythonOk)("Python: both compile, the imports resolve in sdk/python and every client.<resource>.<method> exists", () => {
    const dir = dirFor("python", set);
    const s = snippets("python", inputs);
    file(dir, "send.py", s.send);
    file(dir, "webhook.py", s.webhook);
    const env = { PYTHONPATH: join(repo, "sdk/python/src"), PYTHONDONTWRITEBYTECODE: "1" };
    for (const name of ["send.py", "webhook.py"]) {
      const c = run(python, ["-m", "py_compile", join(dir, name)], { cwd: dir, env });
      expect(out(c), name).toBe("");
    }
    const r = run(python, [checks("check.py"), join(dir, "send.py"), join(dir, "webhook.py")], { cwd: dir, env });
    expect(out(r)).toBe("ok\n");
  });

  const go = gate("go", works("go", ["version"]));
  it.skipIf(!go)("Go: send and webhook vet in a module that replaces the SDK with sdk/go", () => {
    const dir = dirFor("go", set);
    const s = snippets("go", inputs);
    file(dir, "go.mod", `module snippet\n\ngo 1.23\n\nrequire github.com/0furkancolak/payrudder/sdk/go v0.0.0\n\nreplace github.com/0furkancolak/payrudder/sdk/go => ${join(repo, "sdk/go")}\n`);
    file(dir, "main.go", s.send);
    // The webhook snippet is the body of an HTTP handler: body is the raw request body, r and w are the request and the writer.
    file(dir, "hook.go", `package main\n\nimport (\n\t"fmt"\n\t"net/http"\n\t"os"\n\n\tpayrudder "github.com/0furkancolak/payrudder/sdk/go"\n)\n\nfunc hook(w http.ResponseWriter, r *http.Request, body []byte) {\n${s.webhook}\n}\n\nvar _ = hook\nvar _, _ = fmt.Sprint, os.Getenv\n`);
    const r = run("go", ["vet", "./..."], { cwd: dir });
    expect(out(r)).toBe("");
  });

  const phpNative = works("php", ["-v"]);
  const phpDocker = !phpNative && works("docker", ["image", "inspect", "php:8.4-cli"]);
  const phpOk = gate("php (or docker with php:8.4-cli)", phpNative || phpDocker);
  it.skipIf(!phpOk)("PHP: both lint, and every class and method they use is in sdk/php/src", () => {
    const dir = dirFor("php", set);
    const s = snippets("php", inputs);
    file(dir, "send.php", `<?php\n${s.send}\n`);
    file(dir, "webhook.php", `<?php\n${s.webhook}\n`);
    // Natively the SDK is where it is. In Docker (php:8.4-cli, as `make sdk-php-check` does) it is mounted at /sdk, the snippets at /w and the check script at /c.
    const sdk = phpNative ? join(repo, "sdk/php") : "/sdk";
    const at = (p: string) => (phpNative ? p : p.replace(dir, "/w"));
    const php = (args: string[]) =>
      phpNative
        ? run("php", args, { cwd: dir })
        : run("docker", ["run", "--rm", "-v", `${join(repo, "sdk/php")}:/sdk:ro`, "-v", `${dir}:/w:ro`, "-v", `${checks("check.php")}:/c/check.php:ro`, "-w", "/w", "php:8.4-cli", "php", ...args]);
    for (const name of ["send.php", "webhook.php"]) expect(out(php(["-l", at(join(dir, name))])), name).toMatch(/^No syntax errors detected/);
    const r = php(["-d", "zend.assertions=1", "-d", "assert.exception=1", phpNative ? checks("check.php") : "/c/check.php", sdk, at(join(dir, "send.php")), at(join(dir, "webhook.php"))]);
    expect(out(r)).toBe("ok\n");
  });
});

describe("the curl command, run against a server that answers like the API", () => {
  const bash = gate("bash and curl", works("bash", ["-c", "command -v curl"]));
  it.skipIf(!bash).each([
    ["typical", normal],
    ["hostile", hostile],
  ])("%s values reach the API as the JSON they were typed as", async (_set, inputs) => {
    const seen: { auth: string | undefined; body: string }[] = [];
    const server = createServer((req, res) => {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => {
        seen.push({ auth: req.headers.authorization, body });
        res.writeHead(202, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ results: [{ status: "accepted" }] }));
      });
    });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    try {
      const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
      const s = snippets("curl", { ...inputs, baseUrl: base });
      // Asynchronously: the server above lives in this process and would never answer while it waits for the child.
      const r = await new Promise<{ code: number | null; text: string }>((resolve) => {
        const child = execFile("bash", ["-c", `set -e\n${s.send}`], { env: { ...process.env, PAYRUDDER_API_KEY: "qk_test_run" }, timeout: 30_000 }, (err, stdout, stderr) => resolve({ code: err ? ((err as { code?: number }).code ?? 1) : 0, text: stdout + stderr }));
        child.stdin?.end();
      });
      expect(r.code, r.text).toBe(0);
      expect(seen).toHaveLength(1);
      expect(seen[0]?.auth).toBe("Bearer qk_test_run");
      const event = (JSON.parse(seen[0]?.body ?? "{}") as { events: Record<string, string>[] }).events[0];
      // Newlines are folded to a space on purpose (a newline in a JSON string is not valid), every other character arrives as typed.
      expect(event?.customer_external_id).toBe(inputs.customer.replace(/\r?\n/g, " "));
      expect(event?.meter_code).toBe(inputs.meter.replace(/\r?\n/g, " "));
      expect(event?.event_time).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
    } finally {
      server.close();
    }
  });

  it("is checked for every language the page offers", () => {
    expect([...LANGS].sort()).toEqual(["curl", "go", "php", "python", "typescript"]);
  });
});
