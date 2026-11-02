#!/usr/bin/env node
// Release readiness of the four SDKs. It publishes nothing, tags nothing and talks to no registry: it only reads and (for `bump`) edits files in the working tree.
//
//   node scripts/sdk-release.mjs check            the version is the same everywhere it is written, every SDK has its LICENSE and CHANGELOG, the manifests carry the
//                                                 metadata a registry shows, and a package that is still 0.0.0 cannot be published by accident (TypeScript: private)
//   node scripts/sdk-release.mjs check --publish  the same, and refuse 0.0.0 (the pre-release marker) and an undated changelog: what .github/workflows/sdk-publish.yml
//                                                 runs before it does anything but a dry run
//   node scripts/sdk-release.mjs bump X.Y.Z       write X.Y.Z in every place, date the changelogs, and make the TypeScript package publishable
//   node scripts/sdk-release.mjs version         print the common version
//
// One version number for all four SDKs and the Go command line (default, not agreed): they are generated from one OpenAPI file and move together.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const PRE_RELEASE = "0.0.0";
const SEMVER = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

/** Every place a version is written: where it is and how to read it (the JSON files are replaced by `bump` separately, below). */
const PLACES = [
  { file: "sdk/go/client.go", re: /(const Version = ")([^"]+)(")/ },
  { file: "sdk/typescript/src/version.ts", re: /(export const VERSION = ")([^"]+)(")/ },
  { file: "sdk/python/src/payrudder/_version.py", re: /(__version__ = ")([^"]+)(")/ },
  { file: "sdk/php/src/Client.php", re: /(public const VERSION = ')([^']+)(')/ },
  { file: "sdk/python/pyproject.toml", re: /(^version = ")([^"]+)(")/m },
  { file: "sdk/typescript/package.json", get: (d) => d.version },
  // The lock file names the root package twice.
  { file: "sdk/typescript/package-lock.json", get: (d) => d.version },
  { file: "sdk/typescript/package-lock.json", get: (d) => d.packages[""].version },
];

const SDKS = ["go", "typescript", "python", "php"];

const read = (root, f) => fs.readFileSync(path.join(root, f), "utf8");
const write = (root, f, s) => fs.writeFileSync(path.join(root, f), s);

function versionAt(root, place) {
  const text = read(root, place.file);
  return place.get ? place.get(JSON.parse(text)) : text.match(place.re)?.[2];
}

/** The version of every place, for the report and the comparison. */
export function versions(root) {
  return PLACES.map((p) => ({ file: p.file, version: versionAt(root, p) }));
}

/** Problems with the release readiness of the SDKs under root, as a list of sentences. Empty: ready. */
export function check(root, { publish = false } = {}) {
  const problems = [];
  const found = versions(root);
  for (const f of found) if (!f.version) problems.push(`${f.file}: no version found`);
  const distinct = [...new Set(found.map((f) => f.version).filter(Boolean))];
  if (distinct.length > 1) {
    problems.push(`the SDKs disagree on the version: ${found.map((f) => `${f.file} ${f.version}`).join(", ")}`);
  }
  const version = distinct[0];
  if (version && !SEMVER.test(version)) problems.push(`"${version}" is not a semantic version (X.Y.Z)`);
  const released = version !== undefined && version !== PRE_RELEASE;
  if (publish && !released) problems.push(`the version is ${PRE_RELEASE}, the pre-release marker: nothing may be published until \`bump\` has set a real version`);

  const rootLicense = read(root, "LICENSE");
  for (const sdk of SDKS) {
    const dir = `sdk/${sdk}`;
    if (!fs.existsSync(path.join(root, dir, "LICENSE")) || read(root, `${dir}/LICENSE`) !== rootLicense) {
      problems.push(`${dir}/LICENSE must be a copy of the LICENSE at the repository root (a registry package ships its own)`);
    }
    problems.push(...checkChangelog(root, `${dir}/CHANGELOG.md`, released ? version : undefined, publish));
    if (released && /pre-?release/i.test(read(root, `${dir}/README.md`).split("\n").slice(0, 12).join("\n"))) {
      problems.push(`${dir}/README.md still opens with a pre-release notice but the version is ${version}`);
    }
  }

  // What a registry shows, and the guard that keeps 0.0.0 off it.
  const ts = JSON.parse(read(root, "sdk/typescript/package.json"));
  if ((ts.private === true) !== !released) {
    problems.push(released ? "sdk/typescript/package.json is still private: npm would refuse to publish it" : "sdk/typescript/package.json must be private while the version is 0.0.0: it is what keeps `npm publish` from working by accident");
  }
  if (ts.license !== "Elastic-2.0") problems.push('sdk/typescript/package.json: license must be the SPDX id "Elastic-2.0"');
  for (const k of ["repository", "homepage", "bugs", "keywords"]) if (!ts[k]) problems.push(`sdk/typescript/package.json: ${k} is missing`);
  if (ts.repository?.directory !== "sdk/typescript") problems.push("sdk/typescript/package.json: repository.directory must be sdk/typescript");
  if (ts.publishConfig?.access !== "public") problems.push("sdk/typescript/package.json: publishConfig.access must be public (a scoped package is private by default)");
  for (const f of ["dist", "README.md", "LICENSE", "CHANGELOG.md"]) if (!ts.files?.includes(f)) problems.push(`sdk/typescript/package.json: files must include ${f}`);

  const py = read(root, "sdk/python/pyproject.toml");
  for (const k of ["Homepage", "Repository", "Changelog", "Issues"]) if (!new RegExp(`^${k} = "https://`, "m").test(py)) problems.push(`sdk/python/pyproject.toml: [project.urls] ${k} is missing`);
  if (!/^include = \[.*"LICENSE".*"CHANGELOG\.md"/m.test(py)) problems.push("sdk/python/pyproject.toml: the sdist must include LICENSE and CHANGELOG.md");

  const php = JSON.parse(read(root, "sdk/php/composer.json"));
  if ("version" in php) problems.push("sdk/php/composer.json must not have a version: Composer takes it from the git tag, and a fixed one disagrees with the tag");
  if (!php.homepage || !php.support?.issues) problems.push("sdk/php/composer.json: homepage and support.issues are missing");
  return problems;
}

function checkChangelog(root, file, version, publish) {
  if (!fs.existsSync(path.join(root, file))) return [`${file} is missing`];
  const text = read(root, file);
  const out = [];
  const unreleased = text.indexOf("## [Unreleased]");
  if (unreleased < 0) out.push(`${file}: no "## [Unreleased]" section`);
  if (version) {
    const m = text.match(new RegExp(`^## \\[${version.replaceAll(".", "\\.")}\\] - (\\d{4}-\\d{2}-\\d{2})$`, "m"));
    if (!m) out.push(`${file}: no "## [${version}] - YYYY-MM-DD" section for the version being released`);
    else if (unreleased >= 0 && text.indexOf(m[0]) < unreleased) out.push(`${file}: the Unreleased section must come first`);
  }
  if (publish && version && unreleased >= 0) {
    // Whatever is still under Unreleased was not part of what is about to be published: say so rather than ship a changelog that omits it.
    const body = text.slice(unreleased + "## [Unreleased]".length).split(/^## \[/m)[0].trim();
    if (body) out.push(`${file}: the Unreleased section still has entries; they belong under ${version} or must wait`);
  }
  return out;
}

/** Writes version into every place and dates the changelogs. Returns the files it changed. */
export function bump(root, version, today = new Date().toISOString().slice(0, 10)) {
  if (!SEMVER.test(version)) throw new Error(`"${version}" is not a semantic version (X.Y.Z)`);
  if (version === PRE_RELEASE) throw new Error(`${PRE_RELEASE} is the pre-release marker, not a version to release`);
  const current = [...new Set(versions(root).map((v) => v.version))];
  if (current.length !== 1) throw new Error(`the SDKs disagree on the current version (${current.join(", ")}): fix that first`);
  if (current[0] === version) throw new Error(`the version is ${version} already`);

  const changed = [];
  for (const p of PLACES.filter((x) => x.re)) {
    write(root, p.file, read(root, p.file).replace(p.re, `$1${version}$3`));
    changed.push(p.file);
  }
  const editJSON = (file, mutate) => {
    const d = JSON.parse(read(root, file));
    mutate(d);
    write(root, file, JSON.stringify(d, null, 2) + "\n");
    changed.push(file);
  };
  editJSON("sdk/typescript/package.json", (d) => {
    d.version = version;
    delete d.private; // 0.0.0 is private so that it cannot be published; a real version must be able to be
    d.description = d.description.replace(/ \(pre-release\)$/, "");
  });
  editJSON("sdk/typescript/package-lock.json", (d) => {
    d.version = version;
    d.packages[""].version = version;
  });
  for (const sdk of SDKS) {
    const f = `sdk/${sdk}/CHANGELOG.md`;
    write(root, f, read(root, f).replace("## [Unreleased]", `## [Unreleased]\n\n## [${version}] - ${today}`));
    changed.push(f);
  }
  return changed;
}

function main(argv) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const [cmd, ...rest] = argv;
  switch (cmd) {
    case "check": {
      const problems = check(root, { publish: rest.includes("--publish") });
      if (problems.length) {
        console.error(problems.map((p) => `- ${p}`).join("\n"));
        console.error(`\n${problems.length} problem(s): the SDKs are not release-ready.`);
        return 1;
      }
      console.log(`SDK release readiness ok (version ${versions(root)[0].version}${rest.includes("--publish") ? ", publishable" : ""}).`);
      return 0;
    }
    case "bump": {
      const changed = bump(root, rest[0] ?? "");
      console.log(`${changed.join("\n")}\n\nVersion set. Still by hand: the "Pre-release" notice at the top of each SDK README, the install lines that say "not on <registry> yet",`);
      console.log("and the same in docs/18-sdks.md and the console's Connect page. Then run `node scripts/sdk-release.mjs check --publish`. Nothing was tagged or published.");
      return 0;
    }
    case "version":
      console.log(versions(root)[0].version);
      return 0;
    default:
      console.error("usage: node scripts/sdk-release.mjs check [--publish] | bump X.Y.Z | version");
      return 2;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  }
}
