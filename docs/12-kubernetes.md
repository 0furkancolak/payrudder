# Kubernetes (kind + Helm)

Two charts in `deploy/helm/`:

| Chart | What it holds |
|---|---|
| `quotify-infra` | Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO and Mailpit as plain StatefulSets/Deployments, the topics Job (`rpk`), and the init script that creates a database and three roles per service. Every part has an `enabled` flag: a managed service replaces it by pointing the other chart at an address. |
| `quotify` | The services (gateway, plans, ingest, aggregator, limits, billing, analytics, assets, the three workers) and mock-psp. One template loops over `values.yaml: services`, so adding a service is a values entry, not a new template. |

They are separate releases on purpose: the migration Jobs of `quotify` are pre-install hooks, and hooks run before the release's other
resources exist, so the databases they migrate must belong to an earlier release.

## Run it

```bash
make kind-up                       # 1 control plane + 3 workers (deploy/kind/cluster.yaml)
make kind-images                   # build the 12 images (one at a time) and load them into the nodes
make helm-install                  # quotify-infra (3-node Redpanda and RabbitMQ), then quotify (2 replicas of the busy services)
make netpol-test e2e-kind          # NetworkPolicy enforcement, then the e2e suite against http://localhost:8000
```

A Docker VM with less than about 6 GiB cannot hold the three-worker cluster. Use the one-node profile instead:
`make kind-up KIND_CONFIG=deploy/kind/cluster-min.yaml kind-images helm-install KIND_PROFILE=kind-min`
(one replica of everything, single-node Redpanda and RabbitMQ; it fits in about 3 GiB).

The kind node maps the host ports 8000 (gateway), 8090 (mock-psp), 8025 (Mailpit), 9001 (MinIO S3, the public endpoint in presigned URLs) and
31092-31094 (the Redpanda brokers, advertised to clients outside the cluster as `localhost:3109x`).

## Values

| File | For |
|---|---|
| `quotify/values.yaml` | The defaults: every address is a value (`global.kafkaBrokers`, `global.redisURL`, `postgres.host`, `rabbitmq.host`, `s3.endpoint`, `smtp.addr`, `clickhouse.addr`). |
| `values-kind.yaml` / `values-kind-min.yaml` | kind: replicas, HPAs on the gateway and ingest-svc, the NodePorts for the host. |
| `values-sandbox.yaml` | Sandbox-only install ([ADR 0011](adr/0011-sandbox-environment.md)): `QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox` (a live account is refused), `PAYMENTS_LIVE_PROVIDER` empty, receipts to Mailpit. Install it in its own namespace: `helm install quotify deploy/helm/quotify -n quotify-sandbox -f deploy/helm/quotify/values-sandbox.yaml`. |
| `quotify-infra/values-ha.yaml` | Three Redpanda brokers (topics at RF 3) and three RabbitMQ nodes (quorum queues). |

**External S3** is values-only: `--set s3.endpoint=https://s3.eu-west-1.amazonaws.com --set s3.bootstrap.enabled=false --set s3.forcePathStyle=false` and
`--set minio.enabled=false` on `quotify-infra`. A render test asserts that no MinIO object remains in either chart.

## How it is put together

- **Migrations** are `pre-install,pre-upgrade` Jobs per schema, running the service's own image with `QUOTIFY_MIGRATE_ONLY=true`, so a rollout applies
  the migrations of the new version before any new pod starts. The Job reads a hook Secret that holds the schema owner's DSN; the Deployments do not
  get that role at all (`QUOTIFY_MIGRATE_ON_START=false`).
- **Secrets**: one Secret per service, rendered from values (role DSNs with URL-escaped passwords, broker and S3 credentials, the operator token). The
  defaults are the local `*_local` passwords; set `postgres.passwords.<role>`, `rabbitmq.password`, `s3.secretKey`, `operatorToken` for anything real.
- **gRPC balancing**: the gRPC Services are headless, so `dns:///plans-svc:7001` resolves to every pod and the client's round-robin policy spreads calls.
- **Probes** are `/healthz` and `/readyz` on each service's metrics port (mock-psp serves `/healthz` on its API port). Pods run as the distroless
  `nonroot` user (65532), read-only root filesystem, no capabilities, with a `/tmp` emptyDir.
- **NetworkPolicy**: a default-deny for ingress to Quotify's pods, then one policy per service naming its only callers (for example `ingest-svc:7002`
  accepts the gateway and billing-svc; mock-psp accepts payments-worker). The gateway is the only service open to everyone, and the only NodePort besides mock-psp
  on kind. `make netpol-test` proves it on a running cluster: a pod with the gateway's labels connects, an unlabelled pod and a pod in another
  namespace do not. kindnet enforces policies; infrastructure pods are not restricted.
- **HPA and PDB**: the gateway and ingest-svc scale on CPU (`values-kind.yaml`); every service with more than one replica gets a PodDisruptionBudget
  (`maxUnavailable: 1`). The consumers are capped by their partition counts and are not autoscaled.
- **Rolling updates** use `maxUnavailable: 0, maxSurge: 1`; a changed Secret or setting rolls the pods through a checksum annotation.

## Tests

- `tests/helm` renders both charts with `bin/helm` and asserts the safety properties: the sandbox profile has no live provider and allows only sandbox,
  external S3 has no MinIO, the NetworkPolicies name the right callers, migrations are hooks and their role never reaches a running pod, probes,
  resources, PDBs and HPAs exist, role passwords are URL-escaped, and the copies of shared files in the infra chart match their sources.
- CI runs `make helm-lint` (`helm lint` + `kubeconform` for every values file), `ct lint`, those render tests, and a `kind` job that does the
  three-worker install and runs `netpol-test` and `e2e-kind`.

## Not covered

- Highly available Postgres: one StatefulSet. CloudNativePG is the production path and is a replacement for the `postgres` component, not a change to the services.
- The observability stack (collector, Tempo, Prometheus, Grafana) on kind. The services emit OTLP when `global.otlpEndpoint` is set; the compose `obs` profile is the supported setup.
- Ingress and TLS, a service mesh, GitOps: next steps, deliberately out of scope.
