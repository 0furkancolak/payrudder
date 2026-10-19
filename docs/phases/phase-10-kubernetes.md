# Phase 10 — Kubernetes (kind + Helm)

## Goal

Run the whole platform on a local kind cluster through Helm, with production-shaped manifests (probes, resources,
disruption budgets, HPA), and pass the chaos suite using pod deletion.

## Scope

**In:** `deploy/kind/cluster.yaml`, umbrella Helm chart `deploy/helm/quotify` with one subchart per service, infra via upstream
charts or operators (Redpanda, RabbitMQ cluster operator, CloudNativePG, Redis, ClickHouse, MinIO, Mailpit), a migrations Job per
service, NetworkPolicies, HPA, PDBs, `make kind-up helm-install`, chaos via `kubectl delete pod`, mock-psp as a chart component,
S3 configuration via values (in-cluster MinIO by default), and a sandbox-only deployment profile (`values-sandbox.yaml`, [ADR 0011](../adr/0011-sandbox-environment.md)).

**Out of scope:** cloud deploys, Ingress TLS, service mesh/mTLS, GitOps (documented as next steps).

## Tasks

- [ ] kind cluster with 3 worker nodes, local registry, `make kind-up`
- [ ] Infra install script (Helm repos pinned): CloudNativePG cluster, Redpanda (3 brokers, RF 3), RabbitMQ cluster (3 nodes, quorum), Redis, ClickHouse, MinIO, Mailpit, observability stack
- [ ] Service subchart template: Deployment, Service (headless for gRPC), ConfigMap/Secret, probes from `/healthz` `/readyz`, resources, PDB
- [ ] Migration `Job`s as Helm pre-install/pre-upgrade hooks
- [ ] Topic creation Job (`rpk`) and RabbitMQ topology via definitions
- [ ] HPA: gateway/ingest on CPU. Consumers capped at the partition count
- [ ] NetworkPolicy: only gateway reachable from outside the namespace. Internal gRPC only from allowed callers
- [ ] `values-kind.yaml` with replicas matching the chaos profile (sandbox + live tenants, mock-psp deployed)
- [ ] `values-sandbox.yaml`: namespace `quotify-sandbox`, `QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox`, `PAYMENTS_LIVE_PROVIDER` unset, SMTP → Mailpit, shorter Kafka/ClickHouse retention, `free` tier defaults
- [ ] S3 values (`s3.endpoint`, `s3.bucket`, `s3.existingSecret`, `s3.forcePathStyle`). MinIO bucket + lifecycle via a Helm hook Job. Switching to an external S3 is values-only
- [ ] mock-psp subchart (Deployment + its Postgres DB in the CNPG cluster), reachable only from payments-worker (NetworkPolicy)
- [ ] `make demo-k8s`: the sandbox demo flow against the kind cluster
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
- [ ] `make demo-k8s` on kind: sandbox `ClosePeriodNow` → every `pm_sandbox_*` scenario reaches its expected saga state, PDFs in in-cluster MinIO, emails in Mailpit
- [ ] `values-sandbox.yaml` install: creating a live account is rejected, and no pod has a non-mock payment provider configured (rendered-manifest test)
- [ ] Rendered manifests with `s3.endpoint` pointing at an external S3 have no MinIO resources (template test)
- [ ] CI: `helm lint`, `ct lint`, `kubeconform` pass for `values-kind.yaml` and `values-sandbox.yaml`

## Dependencies

Phase 09 (chaos suite, stable services).

## Risks / open questions

- Laptop resources for 3-node Redpanda + 3-node RabbitMQ + CNPG + ClickHouse. Fallback: single-node infra values with a separate "HA" values file run in CI only.
- Operators vs. plain charts for infra: planned operators for Postgres/RabbitMQ (realistic), plain charts for the rest.

## Notes (as built)

- **Two charts, not an umbrella with a subchart per service.** `quotify-infra` (plain StatefulSets: Postgres, Redpanda, RabbitMQ with k8s
  peer discovery, Redis, ClickHouse, MinIO, Mailpit, the topics Job) and `quotify` (one template looping over `values.yaml: services`). Two
  releases because the pre-install migration Jobs need their databases to exist. Postgres is one StatefulSet, not CloudNativePG: the
  operator is the production path and replaces the `postgres` component; it was not installed here. Details: [12-kubernetes.md](../12-kubernetes.md).
- **No local registry**: `make kind-images` builds the images one at a time and `kind load`s them. Twelve parallel Go builds exhaust a 3 GiB Docker VM.
- **`QUOTIFY_MIGRATE_ONLY`** (in `config.Base`, honoured by every service that migrates) makes the service's own image the migration Job.
- **Verified on this laptop** with the one-node profile (`cluster-min.yaml`, `values-kind-min.yaml`, 3 GiB): the install, `make netpol-test` (a pod with the
  gateway's labels reaches `ingest-svc:7002`; an unlabelled pod and a pod in another namespace do not) and `make demo-k8s` (the sandbox billing and payment
  scenarios, PDFs, receipts). The three-worker, three-broker cluster, `e2e-kind`, `rollout-test` and `chaos-kind` are wired into CI and the Makefile but were
  not run on this machine (the Docker VM cannot hold them); the `kind` job in `ci.yml` runs the first three.
- Chaos on Kubernetes: `QUOTIFY_CHAOS_PLATFORM=kubernetes` makes the monkey delete pods (including a Redpanda broker and Redis), and partition services with
  a temporary deny-all NetworkPolicy in place of Toxiproxy (`make chaos-kind`).
- Not done: an observability stack on kind; a `values-sandbox.yaml` namespace install is rendered and tested, not run.
