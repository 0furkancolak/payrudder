# Phase 24 — Foundations: brand, config, error keys, licence

See [17-platform-direction.md](../17-platform-direction.md), [ADR 0019](../adr/0019-license.md) and [ADR 0020](../adr/0020-modules-cqrs-errors-config.md).

## Scope

- `pkg/brand`: product name and slug, read by e-mails, the PDF, the hosted page, webhook headers and the console (served by the gateway's `/v1/brand`). `tools/rename` rewrites the internal identifiers (module path, env prefix, wire names) from a mapping and fails if anything is left.
- `pkg/config`: embedded `config.default.yaml` per service, environment overrides, a test that every YAML key maps to a field and every required secret is absent from YAML.
- `pkg/errkey`: error with a key; gateway returns `error.key`; `docs/error-keys.md` generated; a test that returned keys are listed. Existing errors get keys where the response is already tested.
- SQL rule (ADR 0020 §5): `depguard` forbids query builders, a test rejects SQL built with `fmt.Sprintf`, and list endpoints return `count(*) OVER() AS _count` from the same query. Existing hand-written SQL moves to sqlc module by module; the first to go are `billing-svc/internal/api/reports.go` (SQL built with `Sprintf`), `pkg/outbox` (table name interpolated) and the list queries that count in a second statement. ClickHouse queries in analytics-svc build a bucket expression with `Sprintf`; they become one fixed statement per bucket size.
- Licence: Elastic License 2.0, `NOTICE`, README wording.

## Exit criteria

Renaming the brand changes every visible string without a code change; a service starts from its YAML defaults alone plus its secrets; every error in a response has a key; the licence files are in place.

## Not in scope

Renaming the module path in this repository (the script exists and is tested on a copy; running it is the owner's choice of the new name).
