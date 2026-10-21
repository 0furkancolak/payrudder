# Kubernetes (kind + Helm)

Two charts in `deploy/helm/`:

| Chart | What it holds |
|---|---|
| `quotify-infra` | Postgres, Redpanda, RabbitMQ, Redis, ClickHouse, MinIO and Mailpit as plain StatefulSets/Deployments, the topics Job (`rpk`), and the init script that creates a database and three roles per service. Every part has an `enabled` flag: a managed service replaces it by pointing the other chart at an address. |
| `quotify` | The services (gateway, plans, ingest, aggregator, limits, billing, analytics, assets, the three workers) and mock-psp. One template loops over `values.yaml: services`, so adding a service is a values entry, not a new template. |

Not on Kubernetes? [deploy/railway](../deploy/railway/README.md) runs the same services on Railway ([phase 31](phases/phase-31-railway.md)).

They are separate releases on purpose: the migration Jobs of `quotify` are pre-install hooks, and hooks run before the release's other
resources exist, so the databases they migrate must belong to an earlier release.

## Run it

```bash
make kind-up                       # 1 control plane + 3 workers (deploy/kind/cluster.yaml)
make kind-images                   # build the 12 images (one at a time) and load them into the nodes
make helm-install                  # quotify-infra (3-node Redpanda and RabbitMQ), then quotify (2 replicas of the busy services)
make netpol-test e2e-kind          # NetworkPolicy enforcement, then the e2e suite against http://localhost:42800
```

A Docker VM with less than about 6 GiB cannot hold the three-worker cluster. Use the one-node profile instead:
`make kind-up KIND_CONFIG=deploy/kind/cluster-min.yaml kind-images helm-install KIND_PROFILE=kind-min`
(one replica of everything, single-node Redpanda and RabbitMQ; it fits in about 3 GiB).

The kind node maps the host ports 42800 (gateway), 42890 (mock-psp), 42825 (Mailpit), 42901 (MinIO S3, the public endpoint in presigned URLs) and
31092-31094 (the Redpanda brokers, advertised to clients outside the cluster as `localhost:3109x`).

## Install on your own cluster

```bash
cp deploy/helm/quotify/values-secrets.example.yaml my-secrets.yaml      # change EVERY value; keep the file out of git
make k8s-install REGISTRY=ghcr.io/you/ TAG=v0.5.0 SECRETS=my-secrets.yaml
kubectl -n quotify port-forward svc/gateway 8000:8000                   # or put an Ingress in front of the gateway Service
make k8s-uninstall                                                      # the volumes stay until you delete the namespace
```

`k8s-install` applies `quotify-infra` (single replica of everything, or add `-f deploy/helm/quotify-infra/values-ha.yaml` through `HELM_ARGS` for three Redpanda and RabbitMQ nodes) and then `quotify` with `values-small.yaml`: one replica, 128 MiB limits, the gateway as a ClusterIP Service,
no mock provider. The cluster is the one `kubectl` points at (`kubectl config use-context`). Build and push the images first (`docker compose --profile app build`, then tag `quotify/<service>` with your registry and push); a managed Postgres, Redis, Kafka, RabbitMQ or S3 replaces its part of `quotify-infra` through values (see below).
Each service reads its container memory limit and tells the Go runtime to collect before reaching 90 % of it (`pkg/memlimit`), so a small limit means more collecting, not a kill.

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
  accepts the gateway and billing-svc; mock-psp accepts payments-worker; payments-worker accepts only the gateway, which forwards provider webhooks and the operator endpoints for provider accounts to it). The gateway is the only service open to everyone, and the only NodePort besides mock-psp
  on kind. `make netpol-test` proves it on a running cluster: a pod with the gateway's labels connects, an unlabelled pod and a pod in another
  namespace do not. kindnet enforces policies; infrastructure pods are not restricted.
- **HPA and PDB**: the gateway and ingest-svc scale on CPU (`values-kind.yaml`); every service with more than one replica gets a PodDisruptionBudget
  (`maxUnavailable: 1`). The consumers are capped by their partition counts and are not autoscaled.
- **Rolling updates** use `maxUnavailable: 0, maxSurge: 1`; a changed Secret or setting rolls the pods through a checksum annotation.
  A rollout must not lose requests, and gRPC behind a headless Service makes that harder than it looks: the clients hold the pod addresses they
  resolved, CoreDNS caches the answer for 30 s, and grpc-go re-resolves only after a connection fails. So a terminating pod keeps serving for
  `global.drainSeconds` (35 s, a `preStop` sleep; `terminationGracePeriodSeconds` is that plus 30), `pkg/grpcx` lowers grpc-go's minimum
  re-resolution interval from 30 s to 2 s, and the services on the synchronous path of `POST /v1/events` (gateway, ingest-svc, plans-svc) run two
  replicas. `make rollout-test` upgrades to a new image tag under k6 load and fails on a single failed request. On the shared two-vCPU GitHub runner, with twelve services restarting on four kind nodes, the first runs lost 1.25% of 1270 requests (p99 5 s; probably CPU starvation, not root-caused, and the one-node run on a laptop lost nothing), so CI allows 3% (`ROLLOUT_MAX_FAIL_RATE`); the default budget stays zero, and a bigger machine is the way to hold the strict bar.

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

## Payment providers

payments-worker is an `http` service in the chart (port 8095, only the gateway may call it) besides consuming RabbitMQ. It needs `payments.secretsKey` (32 bytes, base64, which seals tenants' provider credentials) and
`payments.internalToken` (shared with the gateway): both are secret-backed settings, rendered into the services' Secrets like the other secrets and replaceable with `secrets.existingSecrets`. The chart's values are local
defaults: generate your own (`openssl rand -base64 32`). It reaches plans-svc and billing-svc, whose policies accept it. Without a secrets key the worker starts with provider accounts and webhooks off.
