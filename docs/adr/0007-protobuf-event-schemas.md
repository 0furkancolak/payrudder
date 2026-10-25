# ADR 0007 — Protobuf event schemas with `buf breaking`

- Status: Accepted
- Date: 2026-10-17

## Context

Events live up to 400 days and are re-read during replay by consumers newer than the producer that wrote them. Services
deploy independently (rolling updates). gRPC already uses protobuf. We need compile-time types and automatic
compatibility checks.

## Decision

- All Kafka payloads and RabbitMQ commands/replies are **protobuf (proto3)** in `proto/payrudder/{events,commands}/v1`.
- `buf` v2 manages lint, codegen (`protoc-gen-go`, `protoc-gen-go-grpc`) and **`buf breaking` against `main`** in CI (`WIRE_JSON` rules).
- Evolution rules (summary, full list in [07-correctness.md §9](../07-correctness.md#9-schema-evolution-of-events)): additive only within a
  major version; removed fields are `reserved`; no renames; semantic changes → new package `v2` and new topic `*.v2`.
- Every message carries `ce-type` (fully qualified name) in headers. Golden binary fixtures from each release are decoded in CI.
- **No runtime schema registry** in v1.

## Alternatives considered

| Option | Why not |
|---|---|
| JSON | No enforced schema, larger payloads, and type drift between Go structs |
| Avro + schema registry | Strong evolution story, but a second IDL next to gRPC's protobuf and a runtime registry dependency |
| Protobuf + Confluent/Redpanda schema registry | Valuable when external producers exist. Here every producer is in this monorepo, so `buf breaking` gives the same guarantee at build time. Revisit if tenants ever produce directly to Kafka |
| CloudEvents JSON envelope | We borrow the `ce-*` header names for interoperability, but keep binary protobuf payloads |

## Consequences

- One IDL for sync and async contracts, with generated Go types everywhere.
- Breaking changes fail CI before merge.
- Debugging needs a decoder (`payrudderctl decode`, Redpanda Console with proto descriptors) since payloads aren't human-readable.
