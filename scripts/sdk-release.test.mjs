// Tests of scripts/sdk-release.mjs: run with `node --test scripts/`. They work on a copy of the real files in a temporary directory, so they also fail when a manifest
// of the repository drifts from what the script expects.
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { bump, check, versions } from "./sdk-release.mjs";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const FILES = [
  "LICENSE",
  ...["go", "typescript", "python", "php"].flatMap((s) => [`sdk/${s}/LICENSE`, `sdk/${s}/CHANGELOG.md`, `sdk/${s}/README.md`]),
  "sdk/go/client.go",
  "sdk/typescript/src/version.ts",
  "sdk/typescript/package.json",
  "sdk/typescript/package-lock.json",
  "sdk/python/src/payrudder/_version.py",
  "sdk/python/pyproject.toml",
  "sdk/php/src/Client.php",
  "sdk/php/composer.json",
];

function fixture() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sdk-release-"));
  for (const f of FILES) {
    fs.mkdirSync(path.dirname(path.join(dir, f)), { recursive: true });
    fs.copyFileSync(path.join(repo, f), path.join(dir, f));
  }
  return dir;
}
const edit = (dir, f, fn) => fs.writeFileSync(path.join(dir, f), fn(fs.readFileSync(path.join(dir, f), "utf8")));

test("the repository's SDKs are release-ready, and at the pre-release version", () => {
  assert.deepEqual(check(repo), []);
  assert.deepEqual([...new Set(versions(repo).map((v) => v.version))], ["0.0.0"]);
});

test("0.0.0 cannot be published", () => {
  const problems = check(repo, { publish: true });
  assert.equal(problems.length, 1);
  assert.match(problems[0], /pre-release marker/);
  const run = spawnSync("node", ["scripts/sdk-release.mjs", "check", "--publish"], { cwd: repo, encoding: "utf8" });
  assert.equal(run.status, 1);
});

test("a version written differently in one place is found, with the place", () => {
  const dir = fixture();
  edit(dir, "sdk/php/src/Client.php", (s) => s.replace("VERSION = '0.0.0'", "VERSION = '0.0.1'"));
  const problems = check(dir);
  assert.equal(problems.length, 1);
  assert.match(problems[0], /disagree.*sdk\/php\/src\/Client\.php 0\.0\.1/);
});

test("a missing LICENSE, a changed LICENSE and a missing changelog section are found", () => {
  const dir = fixture();
  fs.rmSync(path.join(dir, "sdk/python/LICENSE"));
  edit(dir, "sdk/go/LICENSE", (s) => s + "\nextra");
  edit(dir, "sdk/php/CHANGELOG.md", (s) => s.replace("## [Unreleased]", "## Unreleased"));
  const problems = check(dir).join("\n");
  assert.match(problems, /sdk\/python\/LICENSE must be a copy/);
  assert.match(problems, /sdk\/go\/LICENSE must be a copy/);
  assert.match(problems, /sdk\/php\/CHANGELOG\.md: no "## \[Unreleased\]"/);
});

test("the TypeScript package is private exactly while the version is 0.0.0", () => {
  const dir = fixture();
  edit(dir, "sdk/typescript/package.json", (s) => s.replace('"private": true,', ""));
  assert.match(check(dir).join("\n"), /must be private while the version is 0\.0\.0/);
});

test("the manifests carry what a registry shows", () => {
  const dir = fixture();
  edit(dir, "sdk/typescript/package.json", (s) => s.replace('"access": "public"', '"access": "restricted"'));
  edit(dir, "sdk/python/pyproject.toml", (s) => s.replace(/^Changelog = .*\n/m, ""));
  edit(dir, "sdk/php/composer.json", (s) => s.replace('"license"', '"version": "1.0.0",\n    "license"'));
  const problems = check(dir).join("\n");
  assert.match(problems, /publishConfig\.access must be public/);
  assert.match(problems, /\[project\.urls\] Changelog is missing/);
  assert.match(problems, /composer\.json must not have a version/);
});

test("bump sets the version everywhere, dates the changelogs, opens the TypeScript package, and the result is publishable", () => {
  const dir = fixture();
  const changed = bump(dir, "0.1.0", "2026-12-01");
  assert.ok(changed.includes("sdk/go/client.go") && changed.includes("sdk/typescript/package-lock.json") && changed.includes("sdk/php/CHANGELOG.md"));
  assert.deepEqual([...new Set(versions(dir).map((v) => v.version))], ["0.1.0"]);
  const pkg = JSON.parse(fs.readFileSync(path.join(dir, "sdk/typescript/package.json"), "utf8"));
  assert.equal(pkg.private, undefined);
  assert.doesNotMatch(pkg.description, /pre-release/);
  for (const sdk of ["go", "typescript", "python", "php"]) {
    const log = fs.readFileSync(path.join(dir, `sdk/${sdk}/CHANGELOG.md`), "utf8");
    assert.match(log, /## \[Unreleased\]\n\n## \[0\.1\.0\] - 2026-12-01\n\n### Added/);
  }
  // The READMEs still open with a pre-release notice: the check says so until someone has written the real install lines.
  assert.match(check(dir, { publish: true }).join("\n"), /README\.md still opens with a pre-release notice/);
  for (const sdk of ["go", "typescript", "python", "php"]) edit(dir, `sdk/${sdk}/README.md`, (s) => s.replace(/pre-?release/gi, "stable"));
  assert.deepEqual(check(dir, { publish: true }), []);
});

test("bump refuses what is not a release", () => {
  const dir = fixture();
  assert.throws(() => bump(dir, "1.0"), /not a semantic version/);
  assert.throws(() => bump(dir, "0.0.0"), /pre-release marker/);
  edit(dir, "sdk/php/src/Client.php", (s) => s.replace("VERSION = '0.0.0'", "VERSION = '0.0.1'"));
  assert.throws(() => bump(dir, "0.2.0"), /disagree/);
});

test("a changelog entry left under Unreleased holds a publish back", () => {
  const dir = fixture();
  bump(dir, "0.1.0", "2026-12-01");
  for (const sdk of ["go", "typescript", "python", "php"]) edit(dir, `sdk/${sdk}/README.md`, (s) => s.replace(/pre-?release/gi, "stable"));
  edit(dir, "sdk/go/CHANGELOG.md", (s) => s.replace("## [Unreleased]\n", "## [Unreleased]\n\n### Fixed\n- something not released yet\n"));
  assert.match(check(dir, { publish: true }).join("\n"), /sdk\/go\/CHANGELOG\.md: the Unreleased section still has entries/);
});

test("the command line refuses a bump it cannot do and prints the usage", () => {
  const bad = spawnSync("node", ["scripts/sdk-release.mjs", "bump", "nope"], { cwd: repo, encoding: "utf8" });
  assert.equal(bad.status, 1);
  assert.match(bad.stderr, /not a semantic version/);
  assert.equal(spawnSync("node", ["scripts/sdk-release.mjs"], { cwd: repo, encoding: "utf8" }).status, 2);
  assert.equal(execFileSync("node", ["scripts/sdk-release.mjs", "version"], { cwd: repo, encoding: "utf8" }).trim(), "0.0.0");
  // Nothing above touched the working tree.
  assert.deepEqual([...new Set(versions(repo).map((v) => v.version))], ["0.0.0"]);
});

test("the publish workflow can only be started by hand and defaults to a dry run", () => {
  const text = fs.readFileSync(path.join(repo, ".github/workflows/sdk-publish.yml"), "utf8");
  const on = text.slice(text.indexOf("\non:\n"), text.indexOf("\npermissions:\n"));
  assert.match(on, /^ {2}workflow_dispatch:/m);
  assert.doesNotMatch(on, /^ {2}(push|pull_request|pull_request_target|schedule|release|workflow_run|repository_dispatch|create|workflow_call):/m);
  assert.match(on, /dry_run:[\s\S]*?type: boolean\n\s+default: true/);
  // Every job that could publish waits for a person to approve the release environment.
  for (const job of ["go-publish", "typescript-publish", "python-publish", "php-publish"]) {
    const body = text.slice(text.indexOf(`\n  ${job}:\n`)).split(/\n  [a-z-]+:\n/)[1] ?? "";
    assert.match(body, /if: \$\{\{ !inputs\.dry_run \}\}/, `${job} must be skipped on a dry run`);
    assert.match(body, /environment: release/, `${job} must need the release environment`);
  }
});
