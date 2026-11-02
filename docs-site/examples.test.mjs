import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { readFile, readdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { parse } from "yaml";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// A template is only a template if CI runs it: every folder under examples/ is in the README's table and has a job of the same name in examples.yml, which ends with the smoke test.
test("every framework template under examples/ is listed in its README and smoke-tested by a job of the same name", async () => {
  const dirs = (await readdir(join(root, "examples"), { withFileTypes: true })).filter((e) => e.isDirectory()).map((e) => e.name);
  assert.ok(dirs.length >= 5, `only ${dirs.length} templates found: ${dirs.join(", ")}`);
  const readme = await readFile(join(root, "examples/README.md"), "utf8");
  const jobs = parse(await readFile(join(root, ".github/workflows/examples.yml"), "utf8")).jobs;
  for (const d of dirs) {
    assert.ok(readme.includes(`[\`${d}/\`](${d}/)`), `${d} is not in the table of examples/README.md`);
    assert.ok(existsSync(join(root, "examples", d, "README.md")), `${d} has no README`);
    const steps = JSON.stringify(jobs[d]?.steps ?? []);
    assert.ok(steps.includes(`node examples/smoke.mjs examples/${d} `), `examples.yml has no job "${d}" that runs the smoke test`);
  }
  assert.deepEqual(Object.keys(jobs).sort(), [...dirs].sort(), "a job of examples.yml has no template");
});

// The two things every template must keep (their READMEs say so): the webhook route reads the raw body, and the API key stays on the server.
test("the Next.js and Laravel templates read the webhook body raw and never give the key to the browser", async () => {
  const next = await readFile(join(root, "examples/nextjs/app/webhooks/payrudder/route.ts"), "utf8");
  const code = next.replace(/^\s*\/\/.*$/gm, "");
  assert.match(code, /request\.arrayBuffer\(\)/);
  assert.ok(!/request\.(json|text)\(\)/.test(code), "the webhook route must read the raw bytes");
  const laravel = await readFile(join(root, "examples/laravel/app/Http/Controllers/WebhookController.php"), "utf8");
  assert.match(laravel.replace(/^\s*\/\/.*$/gm, ""), /\$request->getContent\(\)/);
  for (const f of ["examples/nextjs/package.json", "examples/nextjs/next.config.ts", "examples/nextjs/README.md"]) {
    const t = await readFile(join(root, f), "utf8");
    assert.ok(!/NEXT_PUBLIC_PAYRUDDER/.test(t.replace(/do not give it a `NEXT_PUBLIC_` name/, "")), `${f} puts a Payrudder setting in a NEXT_PUBLIC_ variable`);
  }
  // Versions are pinned exactly in both.
  const pkg = JSON.parse(await readFile(join(root, "examples/nextjs/package.json"), "utf8"));
  for (const [name, v] of Object.entries({ ...pkg.dependencies, ...pkg.devDependencies })) assert.ok(name === "@payrudder/sdk" || /^\d+\.\d+\.\d+$/.test(v), `${name} is not pinned: ${v}`);
  const composer = JSON.parse(await readFile(join(root, "examples/laravel/composer.json"), "utf8"));
  assert.match(composer.require["laravel/framework"], /^\d+\.\d+\.\d+$/);
});
