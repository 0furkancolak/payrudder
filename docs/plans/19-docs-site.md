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
