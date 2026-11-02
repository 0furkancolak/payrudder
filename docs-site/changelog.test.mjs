import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { RELEASE_TAG, UNRELEASED_LIMIT, collect, entryOf, render } from "./changelog.mjs";

test("a squash merge's subject is a pull request; anything else is not", () => {
  assert.deepEqual(entryOf("Customers have a language (#240)"), { title: "Customers have a language", pr: 240 });
  assert.deepEqual(entryOf("Fix: a (b) c (#7)\n"), { title: "Fix: a (b) c", pr: 7 });
  assert.equal(entryOf("A commit pushed straight to main"), null);
  assert.equal(entryOf("Mentions #12 in the middle (not a PR)"), null);
});

test("only vX.Y.Z tags are releases", () => {
  for (const t of ["v1.0.0", "v0.10.2", "v1.2.3-rc.1"]) assert.ok(RELEASE_TAG.test(t), t);
  for (const t of ["sdk/go/v0.1.0", "1.0.0", "v1.0", "latest", "v1.0.0+build"]) assert.ok(!RELEASE_TAG.test(t), t);
});

test("with no tags the page says there is no release and lists what was merged", () => {
  const html = render({ available: true, releases: [], unreleased: [{ title: "Add <b>things</b> & more", pr: 5 }, { title: "Second", pr: 4 }] });
  assert.match(html, /data-releases="0">No release has been made yet/);
  assert.match(html, /id="unreleased"/);
  assert.match(html, /Add &lt;b&gt;things&lt;\/b&gt; &amp; more <span class="pr">#5<\/span>/);
  assert.ok(!html.includes("<b>things</b>"));
});

test("with releases the newest comes first, each with its date and entries, then what is unreleased", () => {
  const html = render({
    available: true,
    releases: [{ tag: "v0.2.0", date: "2026-12-01", entries: [{ title: "B", pr: 2 }] }, { tag: "v0.1.0", date: "2026-11-15", entries: [] }],
    unreleased: [{ title: "C", pr: 3 }],
  });
  assert.ok(html.indexOf("v0.2.0") < html.indexOf("v0.1.0"));
  assert.match(html, /v0\.2\.0 <small>2026-12-01<\/small>/);
  assert.match(html, /No pull request is recorded for this release/);
  assert.match(html, /since v0\.2\.0/);
  assert.ok(html.indexOf('id="releases"') < html.indexOf('id="unreleased"'));
});

test("a build without history says so instead of failing", async () => {
  const r = await collect("/nowhere", async () => {
    throw new Error("not a git repository");
  });
  assert.equal(r.available, false);
  assert.match(render(r), /history of the repository was not available/);
});

test("collect reads a real repository: tags in version order, the pull requests between them, and the rest as unreleased", async () => {
  const dir = await mkdtemp(join(tmpdir(), "changelog-"));
  const git = (...args) => execFileSync("git", ["-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", ...args], { encoding: "utf8" });
  git("init", "-q", "-b", "main");
  let n = 0;
  const commit = (subject) => {
    n += 1;
    git("commit", "-q", "--allow-empty", "-m", subject);
  };
  commit("First thing (#1)");
  commit("Pushed straight to main");
  git("tag", "v0.1.0");
  commit("Second thing (#2)");
  commit("Third thing (#3)");
  git("tag", "v0.2.0");
  git("tag", "sdk/go/v0.2.0"); // not a release of the platform
  commit("Fourth thing (#4)");
  await writeFile(join(dir, "x"), String(n));

  const r = await collect(dir);
  assert.equal(r.available, true);
  assert.deepEqual(r.releases.map((x) => x.tag), ["v0.2.0", "v0.1.0"]);
  assert.deepEqual(r.releases[0].entries.map((e) => e.pr), [3, 2]);
  assert.deepEqual(r.releases[1].entries.map((e) => e.pr), [1]);
  assert.match(r.releases[0].date, /^\d{4}-\d{2}-\d{2}$/);
  assert.deepEqual(r.unreleased.map((e) => e.pr), [4]);
});

test("a repository with no tag lists its pull requests as unreleased, at most the limit", async () => {
  const dir = await mkdtemp(join(tmpdir(), "changelog-"));
  const git = (...args) => execFileSync("git", ["-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false", ...args], { encoding: "utf8" });
  git("init", "-q", "-b", "main");
  for (let i = 1; i <= UNRELEASED_LIMIT + 5; i++) git("commit", "-q", "--allow-empty", "-m", `Change ${i} (#${i})`);
  const r = await collect(dir);
  assert.equal(r.releases.length, 0);
  assert.equal(r.unreleased.length, UNRELEASED_LIMIT);
  assert.equal(r.unreleased[0].pr, UNRELEASED_LIMIT + 5);
});
