// The change log of the platform, made at build time from the git history of the repository and nothing else: the tags that name a release and the titles of the
// pull requests merged between them (a squash merge's subject is the PR title followed by "(#123)"). It reads git and writes nothing, and it never creates a tag.
// No release has been made yet, so there are no tags: the page then says so and lists the most recent merged pull requests under "Unreleased". A checkout without
// history (a shallow clone) gives a shorter list, and one without git gives a page that says the history was not available.
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const run = promisify(execFile);

/** The tags that name a release of the platform: v1.2.3, v1.2.3-rc.1. (The SDKs are tagged sdk/go/v1.2.3 and the like, and are not this page's business.) */
export const RELEASE_TAG = /^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;
/** How many pull requests "Unreleased" shows at most. */
export const UNRELEASED_LIMIT = 50;

async function defaultGit(root, args) {
  return (await run("git", ["-C", root, ...args], { maxBuffer: 16 << 20 })).stdout;
}

/** A merge's subject as an entry: the title and the pull request number. A commit without a number is not a pull request and is left out. */
export function entryOf(subject) {
  const m = /^(.*\S)\s+\(#(\d+)\)$/.exec(subject.trim());
  return m ? { title: m[1], pr: Number(m[2]) } : null;
}

/** { available, releases: [{ tag, date, entries }], unreleased: [entries], shallow }. `git` is replaced in tests. */
export async function collect(root, git = defaultGit) {
  let tags;
  try {
    tags = (await git(root, ["tag", "--list", "--sort=-version:refname"])).split("\n").map((t) => t.trim()).filter((t) => RELEASE_TAG.test(t));
  } catch {
    return { available: false, releases: [], unreleased: [] };
  }
  const subjects = async (range, limit) =>
    (await git(root, ["log", "--first-parent", "--format=%s", ...(limit ? ["-n", String(limit * 4)] : []), range])).split("\n").map(entryOf).filter(Boolean);
  const releases = [];
  for (const [i, tag] of tags.entries()) {
    const range = tags[i + 1] ? `${tags[i + 1]}..${tag}` : tag;
    releases.push({ tag, date: (await git(root, ["log", "-1", "--format=%cs", tag])).trim(), entries: await subjects(range) });
  }
  const unreleased = (await subjects(tags[0] ? `${tags[0]}..HEAD` : "HEAD", UNRELEASED_LIMIT)).slice(0, UNRELEASED_LIMIT);
  return { available: true, releases, unreleased };
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const list = (entries) => `<ul>${entries.map((e) => `<li>${esc(e.title)} <span class="pr">#${e.pr}</span></li>`).join("")}</ul>`;

/** The HTML added after the page's introduction. */
export function render({ available, releases, unreleased }) {
  if (!available) return `<p>The history of the repository was not available when this site was built, so there is nothing to list.</p>`;
  const head =
    releases.length === 0
      ? `<h2 id="releases">Releases</h2><p data-releases="0">No release has been made yet: nothing is tagged and nothing is published, so this page has no versions. The pull requests merged so far are listed below as they come.</p>`
      : `<h2 id="releases">Releases</h2>${releases.map((r) => `<h3 id="${esc(r.tag)}">${esc(r.tag)} <small>${esc(r.date)}</small></h3>${r.entries.length ? list(r.entries) : "<p>No pull request is recorded for this release.</p>"}`).join("")}`;
  const tail = unreleased.length
    ? `<h2 id="unreleased">Unreleased</h2><p>Merged into the main branch ${releases.length ? `since ${esc(releases[0].tag)}` : "so far"}, newest first${unreleased.length === UNRELEASED_LIMIT ? ` (the latest ${UNRELEASED_LIMIT})` : ""}. They are not part of any release.</p>${list(unreleased)}`
    : `<h2 id="unreleased">Unreleased</h2><p>Nothing has been merged since${releases.length ? ` ${esc(releases[0].tag)}` : " the start of the history this site was built from"}.</p>`;
  return head + tail;
}
