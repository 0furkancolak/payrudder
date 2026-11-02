# 19 — Documentation site

## The request
List 2 (2): **docs**.

## Where we are
#256: `docs-site/`, a static site built with `marked` and `yaml` from `api/openapi/v1.yaml` and five guides (getting started, webhooks, SDKs, MCP, error keys): 149 operations of the tenant API with parameters, request/response models, example bodies, curl; client-side search; light/dark; tests (every operation has an entry, only the tenant API, no broken link, examples valid) and a CI job that uploads the artifact. Not hosted.

## Gaps
- **Not published.** Where it lives (domain, hosting, auth for private docs) is undecided.
- Content is thin: guides are repo documents (webhooks, SDKs) with repository links turned into plain text; no concepts (metering, packages, subscriptions, invoices, credits, dunning), no tutorials (usage-based billing end to end, migrating from Stripe), no provider guides, no change log, no tax/e-invoice guide, no console user guide, no security page, no status of what is pre-release.
- The API reference has no per-language code tabs, no "try it", no schema search, no deep model pages, descriptions are the OpenAPI text (some mention internal operator routes), errors are a bare key list, no versioning, no redirects, no print/PDF, no i18n (the product has six languages).
- Internal docs (`docs/*.md`) mix audience: public vs maintainer; no separation or review of what is safe to publish.
- No docs for the **operator** and **console** APIs by design.

## Research first
1. Decide the audience(s): developers integrating, tenants' staff using the console, operators, contributors — one site or several.
2. Review `docs/` for what may be public; mark every file public/internal.
3. Look at Stripe, Paddle and Linear docs structure; list pages a billing platform must have.

## Decisions needed
Hosting and domain; whether docs are public or behind sign-in; languages; versioning policy.

## Work
1. Information architecture and a page list (this file updated).
2. Write the concept and tutorial pages; review the OpenAPI descriptions for public wording and fix at the source.
3. Reference upgrades: language tabs from the same generator as the SDK snippets (18), model pages, error catalogue with meanings and fixes, search over models.
4. Publish pipeline (GitHub Pages or the chosen host), preview builds per PR, link checker in CI, versioning.
5. Translations only after the English content is stable.

## Done means
The site is live at the chosen address, every page reviewed by the owner, link checker green, and a new developer can follow a tutorial to a working integration using only the site (tested with a person).

## Not verifiable here
Hosting and real reader feedback.

## Risks
Docs describing unfinished features: every page carries a status.

## Review addendum (2026-11-01)
- Five guide pages and 149 operation pages exist (`docs-site/build.mjs`). Missing: a publish job, a per-page `status`, and a public/internal marker with a test. Default host (not agreed): GitHub Pages, behind a repository variable.

## Progress (2026-11-02, branch `feat/dx-docs-mcp`)

Done:
- **Public or internal, and how finished, for every document.** `docs/publication.yaml` marks each file of `docs/` `public` or `internal` with a status (`stable`, `preview`, `pre-release`, `draft`, `historical`; directories such as `adr`, `plans`, `runbooks` are marked once). The build test fails for a document that is not listed, for a published document that is not `public` and for a `public` one that no page publishes. A manifest rather than a marker in each file, because `docs/error-keys.md` is written by `go test ./pkg/errkey` and would lose a marker. Default (not agreed): only `16-webhooks.md`, `18-sdks.md` and `error-keys.md` are public; everything else stays internal until the owner has read it for publication.
- **Every page shows its status** in a banner under its title (the status of a document of `docs/` comes from the manifest; pages written for the site say theirs in `GUIDES`). The API reference pages say `stable` (the gateway's contract test keeps them equal to the routes); the new concept pages say `draft`: written from the repository's documents, the OpenAPI file and the code, **not reviewed by the owner**.
- **Concept pages:** authentication (keys, environments, roles and permissions, rotation, the refusals), usage metering (meters, events, periods, grace and late events), idempotency (events, the four calls with a header, natural keys), test mode (sandbox, scenarios, closing a period, promote) and errors. A test fails when a page names an API path the OpenAPI file lacks. The sidebar has sections (Start, Concepts, Libraries, API reference).
- **The error catalogue is generated:** every key of `docs/error-keys.md` with its meaning, which is the English message of `errors.<key>` in the console's locale (all keys had one); the test fails for a key without a message. Also the HTTP status of each error code, and the `error_code` of a rejected usage event (from the OpenAPI enum; a test fails for a code without an explanation). Default (not agreed): the keys of `admin`, `identity` and `support` (the operator's and the console's own routes) are left out.
- **Language tabs** (curl, TypeScript, Python, Go, PHP) on `POST /v1/events` (send usage) and `POST /v1/webhook-endpoints` (verify a delivery), made by the console's `snippets.ts` that `build.mjs` imports, so the reference and the Connect page cannot disagree and the install lines are the honest ones (plan 18). Other operations keep their curl: a general operation-to-SDK-call generator is not built.
- **Publish pipeline:** `docs.yml` deploys to GitHub Pages on a push to `main` when the repository variable `DOCS_PUBLISH` is `true`, and not otherwise; every run (a pull request's too) uploads the built site as an artifact, a pull request's named `docs-site-pr-<number>`. Default (not agreed): GitHub Pages.

Not done: tutorials (usage-based billing end to end, migrating from Stripe), provider guides, tax and e-invoice guide, console user guide, security page, change log, "try it", schema search, versioning, redirects, print/PDF, translations, an external-link checker (internal links are checked), reviewing the OpenAPI descriptions for wording that mentions operator routes. Hosting, the domain and whether the docs are public are still the owner's.

Not verified: the Pages deploy (needs Pages switched to the "GitHub Actions" source and the variable set; the workflow has not run on GitHub); the pages were looked at in Chrome on a desktop viewport only; the tabs need JavaScript (without it every language is shown one under the other); the build needs Node 22.18 or newer (it reads `snippets.ts` by stripping its types).


## Progress (2026-11-02, branch `feat/sdk-cli-errors`)

Done:
- **Tutorials** (status `draft`, written from the OpenAPI file, the console's playground and the existing guides, not reviewed by the owner and not run end to end by a person): *Usage-based billing, end to end* (meter, package, customer, subscription, usage, close the period in the sandbox, invoice, a declined payment), *Receive webhooks on your machine* (a verifying handler per language made by the console's snippet generator, the CLI's `listen`, what to look at when a delivery does not arrive) and *Let a customer pay* (invoice link, payment links, the customer's own page). A test reads every `$BASE/v1/...` call in them (path and verb) against the OpenAPI file, so a renamed route fails the build. A link from a guide to the API reference now stays a link (it used to be turned into plain text), and repository documents can link to a site guide by its path (`../docs-site/content/cli.md`).
- **Command line page** (status `pre-release`); a test fails when the program lists a command the page does not show.
- **Change log page**, made at build from git: the tags `vX.Y.Z` and the titles of the pull requests merged between them; with no tag (today) it says there is no release and lists the latest 50 merged pull requests as "Unreleased". `docs.yml` now checks out the full history. Tested against real temporary repositories (tags in version order, SDK tags ignored, no tag, no git).
- **Errors page:** the status of each failure as the four SDKs raise it (Go `Kind*`, TypeScript, Python and PHP classes; a test fails when an SDK gains an error type the page does not map) and what the command line prints and exits with.

Defaults (not agreed): the change log lists pull request titles verbatim; they are written for maintainers (plan numbers, internal names), so read them before the site is hosted; the tutorials use `curl` only, the SDK calls being the same one method per operation.

Not done: *migrating from Stripe*, provider guides, the tax and e-invoice guide, the console user guide, the security page, "try it", schema search, versioning, redirects, print/PDF, translations, an external-link checker, hosting. Not verified: the pages were looked at in Chrome on a desktop viewport only (CLI page and change log); the Pages deploy still has not run.
