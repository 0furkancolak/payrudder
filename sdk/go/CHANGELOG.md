# Changelog

All notable changes to the Payrudder Go SDK and to the `payrudder` command line (`cmd/payrudder`, in the same module). The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), the version numbers follow [Semantic Versioning](https://semver.org/).
The four SDKs and the command line share one version number and are released together (default, not agreed).

Nothing has been published or tagged: the version stays `0.0.0` until the owner lifts the no-release rule (`docs/plans/22-platform-audit-and-roadmap.md`, G16), so everything is
listed under Unreleased. `node scripts/sdk-release.mjs bump X.Y.Z` (at the repository root) moves this section under the new version when a release is prepared.

## [Unreleased]

### Added
- A client for every operation of the tenant API (`/v1/*`): retries with backoff, typed errors, `iter.Seq2` pagination, a batching `Events.Ingest`, and webhook verification (`VerifyWebhook`, `ConstructEvent`). Standard library only; Go 1.23 or newer.
- The `payrudder` command line (`go install github.com/0furkancolak/payrudder/sdk/go/cmd/payrudder`): `login`, `events send`, `events tail`, `webhooks listen`, `webhooks trigger ping`, `webhooks deliveries`, `webhooks list`, `customers list`, `invoices list`.
