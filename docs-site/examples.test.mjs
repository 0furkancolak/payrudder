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
  assert.ok(dirs.length >= 7, `only ${dirs.length} templates found: ${dirs.join(", ")}`);
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

// The Rails and Django templates (plan 18): the same two things, in their own words. Rails has no SDK, so its signature check is written out and is read against the document.
test("the Rails and Django templates read the webhook body raw, keep the webhook outside CSRF, pin the top level, and the Rails check refuses an empty secret", async () => {
  const strip = (t) => t.replace(/^\s*(#|\/\/).*$/gm, "");
  const rails = strip(await readFile(join(root, "examples/rails/app/controllers/webhooks_controller.rb"), "utf8"));
  assert.match(rails, /request\.raw_post/);
  assert.ok(!/params\b/.test(rails), "the Rails webhook route must not read the body from params");
  assert.match(strip(await readFile(join(root, "examples/rails/config/application.rb"), "utf8")), /config\.api_only = true/);
  const verify = await readFile(join(root, "examples/rails/app/lib/payrudder/webhook.rb"), "utf8");
  assert.match(verify, /OpenSSL::HMAC\.hexdigest\("SHA256", secret, signed\)/);
  assert.match(verify, /secure_compare/);
  assert.match(verify, /raise ArgumentError, "PAYRUDDER_WEBHOOK_SECRET is not set" if secret\.to_s\.empty\?/, "an empty secret must stop the check: an HMAC with an empty key is forgeable");
  assert.match(verify, /TOLERANCE = 300/);
  // The tolerance and the signed string are the document's.
  const doc = await readFile(join(root, "docs/16-webhooks.md"), "utf8");
  assert.match(doc, /reject a `t` more than five minutes from your clock/);
  assert.match(doc, /HMAC-SHA256\(secret, "<t>\.<raw body>"\)/);
  const django = strip(await readFile(join(root, "examples/django/payrudder_app/views.py"), "utf8"));
  assert.match(django, /construct_event\(request\.body,/);
  assert.equal((django.match(/@csrf_exempt/g) ?? []).length, 2, "both views opt out of CSRF, the middleware stays on");
  assert.match(await readFile(join(root, "examples/django/payrudder_app/settings.py"), "utf8"), /django\.middleware\.csrf\.CsrfViewMiddleware/);
  const gem = await readFile(join(root, "examples/rails/Gemfile"), "utf8");
  for (const m of gem.matchAll(/^gem "([^"]+)", "([^"]+)"/gm)) assert.match(m[2], /^\d+\.\d+\.\d+$/, `${m[1]} is not pinned in examples/rails/Gemfile`);
  assert.ok((gem.match(/^gem "/gm) ?? []).length >= 2);
  const req = (await readFile(join(root, "examples/django/requirements.txt"), "utf8")).split("\n").filter((l) => l && !l.startsWith("#") && !l.startsWith("."));
  assert.ok(req.length >= 2 && req.every((l) => /^[a-z]+==\d+\.\d+(\.\d+)?$/.test(l)), `requirements are not pinned: ${req.join(", ")}`);
});
