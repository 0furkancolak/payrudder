# Phase 10 — Kubernetes (kind + Helm)

## Goal

Run the whole platform on a local kind cluster through Helm, with production-shaped manifests (probes, resources,
disruption budgets, HPA), and pass the chaos suite using pod deletion.

## Scope

**In:** `deploy/kind/cluster.yaml`, umbrella Helm chart `deploy/helm/quotify` with one subchart per service, infra via upstream
charts or operators (Redpanda, RabbitMQ cluster operator, CloudNativePG, Redis, ClickHouse, MinIO, Mailpit), a migrations Job per
service, NetworkPolicies, HPA, PDBs, `make kind-up helm-install`, chaos via `kubectl delete pod`.

**Out of scope:** cloud deploys, Ingress TLS, service mesh/mTLS, GitOps (documented as next steps).

## Tasks

- [ ] kind cluster with 3 worker nodes, local registry, `make kind-up`
- [ ] Infra install script (Helm repos pinned): CloudNativePG cluster, Redpanda (3 brokers, RF 3), RabbitMQ cluster (3 nodes, quorum), Redis, ClickHouse, MinIO, Mailpit, observability stack
- [ ] Service subchart template: Deployment, Service (headless for gRPC), ConfigMap/Secret, probes from `/healthz` `/readyz`, resources, PDB
- [ ] Migration `Job`s as Helm pre-install/pre-upgrade hooks
- [ ] Topic creation Job (`rpk`) and RabbitMQ topology via definitions
- [ ] HPA: gateway/ingest on CPU. Consumers capped at the partition count
- [ ] NetworkPolicy: only gateway reachable from outside the namespace. Internal gRPC only from allowed callers
- [ ] `values-kind.yaml` with replicas matching the chaos profile
- [ ] Chaos driver variant: random `kubectl delete pod` + broker pod deletion (now multi-node)
- [ ] `helm lint` + `ct` (chart-testing) in CI. `kubeconform` validation

## Deliverables

- `make kind-up && make helm-install && make demo` works from scratch.
- Chaos suite run on kind with report.

## Acceptance criteria

- [ ] Fresh kind install reaches all-ready in < 10 min (target) and `make demo` passes
- [ ] 100k chaos run on kind with random pod deletion (including one Redpanda broker and one RabbitMQ node): A1–A9 pass
- [ ] `helm upgrade` with a new image tag performs a zero-downtime rollout under k6 `ingest_steady` (0 non-429 errors)
- [ ] NetworkPolicy test: a pod in another namespace can't reach `ingest-svc:7002`
- [ ] CI: `helm lint`, `ct lint`, `kubeconform` pass

## Dependencies

Phase 09 (chaos suite, stable services).

## Risks / open questions

- Laptop resources for 3-node Redpanda + 3-node RabbitMQ + CNPG + ClickHouse. Fallback: single-node infra values with a separate "HA" values file run in CI only.
- Operators vs. plain charts for infra: planned operators for Postgres/RabbitMQ (realistic), plain charts for the rest.
