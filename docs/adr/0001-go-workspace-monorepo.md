# ADR 0001 — Go workspace monorepo

- Status: Accepted
- Date: 2026-10-17

## Context

Payrudder has ten services, shared protobuf contracts and a small set of shared libraries (outbox, Kafka/AMQP helpers,
OTel carriers, period/money definitions). There's one developer. Contract changes often touch a producer and its consumers
in the same change, and we want `buf breaking` and cross-service tests to run against one tree.

## Decision

- A single repository `github.com/0furkancolak/payrudder` using a **Go workspace** (`go.work`) on the latest stable Go (1.27).
- One **module per service** (`services/<svc>/go.mod`), plus library modules `pkg`, `gen/go`, `tools`, `tests`.
- `go.work` is committed. Docker builds use the repo root as context so the workspace resolves.
- Protobuf in `proto/`, generated with `buf` into `gen/go` (committed).

## Alternatives considered

| Option | Why not |
|---|---|
| Polyrepo (one repo per service) | Atomic contract changes become multi-PR dances. Too much overhead for one developer, and harder to review as a portfolio piece |
| Monorepo, single `go.mod` | Simplest, but every service inherits every dependency (ClickHouse driver in gateway, fpdf in aggregator). That obscures service boundaries and inflates images |
| Bazel / Pants | Hermetic and fast at scale, but a large learning and maintenance cost not justified at ten services |

## Consequences

- Each service has explicit, minimal dependencies, and `go mod graph` per service reflects its real coupling.
- Cross-module refactors are one PR. `go.work` makes local edits to `pkg` visible immediately.
- Versioning: modules aren't tagged individually. The whole repo is released together (`v0.x`). If a module ever needs to be consumed
  externally, per-module tags (`pkg/v0.3.0`) can be added later.
- CI must run `go work sync` checks and build each module, using a changed-paths matrix to keep CI fast.
- Risk: drifting dependency versions across modules. Mitigation: Renovate with grouped updates, and `make deps-check` lints for mismatched
  versions of shared deps (`franz-go`, `pgx`, `otel`).
