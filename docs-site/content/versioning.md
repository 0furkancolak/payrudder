# Versioning

What carries a version in Payrudder, what does not, and what you can rely on when something changes. This page describes **what exists today**; it is not a compatibility promise. **The owner has not decided a versioning and deprecation policy** (plan 19 lists it as a decision), so where a rule is missing the page says so.

## The API

- **One version, in the path.** Every route of the tenant API starts with `/v1`, and the OpenAPI file says `version: v1`. A request never names a version: there is no version header, no date-based version and no per-account default, and a webhook body has no API version field.
- **Nothing is deprecated, and no sunset is announced.** No route, field or header carries a deprecation marker, and the API sends no `Deprecation` or `Sunset` header.
- **What keeps a change from breaking you today.** The OpenAPI file and the gateway's routes are held equal by a test that fails when a route is missing from either side; the four SDKs and the console's types are generated from the file and a check fails when a generated file is stale; and a pull request that removes or renames a field of the messages the API is built on fails `buf breaking` (wire and JSON compatibility) in CI. A commit that breaks the API or an event schema is written with a `!` and a `BREAKING CHANGE:` footer; the [change log](changelog.html) lists the merged pull requests by title.
- **What is not written down.** An architecture decision says a renamed error key is a breaking change that "follows the API's deprecation rule", but no deprecation rule (how long a route or field is kept, how you are told) is written anywhere in the repository. Until it is, **treat the API as pre-release**: read the [change log](changelog.html) before you upgrade a deployment.
- **Deployment and API move together.** You call your own Payrudder deployment, so its API is the one of the version you deployed. There is no hosted multi-tenant service whose version changes under you; that is a decision about hosting that has not been made.

## Error keys

Each error carries a stable `error.key` ([Errors](errors.html)); translate the key, not the message. A key is part of the API: renaming one counts as a breaking change. The list of keys is generated from the code (`docs/error-keys.md`) and checked by a test, and the console has a message for each.

## Webhook events

The event envelope is exactly `id`, `type`, `created`, `tenant_id` and `data` ([Webhooks](webhooks.html)), and there is no version in the body. The repository does not promise that the set of event types, or the fields in `data`, stay as they are: events have been added (the list in [Webhooks](webhooks.html) grows with the product) and the SDKs type the envelope but leave most `data` payloads untyped. A receiver that ignores a type it does not know and a field it does not use is the safe one; that is advice, not a documented guarantee.

## SDKs and the command line

The four SDKs (TypeScript, Python, Go, PHP) and the `payrudder` command line **share one version number** (a default, not agreed) and are released together. Each keeps a `CHANGELOG.md` in the Keep a Changelog format and says its version numbers follow Semantic Versioning.

**Nothing has been published or tagged.** Every package says `0.0.0`, everything in the changelogs is under *Unreleased*, and the project's rule is that no release or tag is made until the whole platform is finished ([SDKs](sdks.html)). `node scripts/sdk-release.mjs check` verifies that the version is the same in every place it is written and that a `0.0.0` package cannot be published; the publish workflow can only be started by hand, is a dry run unless that is switched off and refuses `0.0.0`. So today there is **no version of an SDK to pin to**: use them from a checkout, at a commit you have tested.

One method exists for each operation of the OpenAPI file, so an SDK upgrade follows the API's changes: a new operation is a new method, and a removed one a removed method.

## Plan versions

A package is a **code** and a series of numbered, immutable **versions** (`GET /v1/plans/{code}/versions/{version}`, where `latest` is allowed). Publishing a package that exists adds a version; a published version is never edited. A subscription keeps the version it started on, so a change of price reaches existing customers only when you move them.

Moving them is a **price change** (`POST /v1/plans/{code}/price-changes`): the subscriptions of one version move to another at their next renewal, no sooner than the notice you give (default 30 days, at least 14, at most 365), and each customer is told by mail in their language. With `effective_from` (a date at least the notice away, at most two years) each subscription moves on its first period start on or after that date instead: a monthly one on the first renewal from it, a yearly one possibly months later. Normally the customer is mailed as soon as the change is scheduled, which can be months before the price starts; with `notice_send_days_before` (0, or at least the notice and at most 365) the mail is held and goes out that many days before the new price starts. It is never later than the customer's notice, a mail that is due already goes at once, withdrawing the price change cancels a held mail, and a pause by the platform operator holds it. A customer in a country where the platform keeps a longer minimum notice (a table the operator edits; it is not legal advice) is told that much earlier, or left out as `renews_soon`. A change can be withdrawn while it waits. A subscription run by a payment provider, one with a change already waiting and one of a blocked customer are left alone and reported. See [Console guide: meters, packages and pricing](console-pricing.html).

## Meter versions

A meter is a code with versions. A new version can change everything except its decimal places, and a distinct-values or latest-value meter keeps that aggregation in every version. Events are checked against the **latest** version of the meter.

## Agreements (contracts)

The agreements a seller shows its customers have numbered versions, each marked *material* or *minor*. A customer accepts a version; `POST /v1/subscriptions` is refused until the customer has accepted the current version of each required kind ([Contracts](contracts.html)).

## Providers' API versions

Payrudder pins the version of each payment provider's API it talks to: every Stripe request carries `Stripe-Version: 2026-09-30.endive` (a Stripe account's default version moving does not change what the adapter reads), and every Paddle request carries `Paddle-Version: 1`. iyzico's API has no version header. The provider guides say what was read from each provider's documentation and what is **UNVERIFIED**.

## This documentation

The sidebar names the API version the site describes (`API v1`, the version of the OpenAPI file it is built from) and the commit it was built from, so a reader of a hosted copy can tell how old it is: nothing is tagged yet, so the commit is the only date there is. The site documents **one** API version, the current one. **Default (not agreed):** when a second path version exists, the pipeline publishes the frozen site of the old version under its own address (`/v1/`) and the current one at the root, with a link between them in the sidebar; until then there is nothing to switch between, and nothing for the sidebar to link to.

An address that was published keeps working. A page that is renamed or removed leaves a small page at its old address that sends the browser (and a search engine, through a canonical link) to the new one, and a test fails when a page of the site disappears without one (`docs-site/pages.txt` lists every address the site has had).
