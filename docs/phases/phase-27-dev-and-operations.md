# Phase 27 — Development, Kubernetes, memory and the edge

## Scope

- `make setup`: install tools, start the infrastructure containers only. `make dev`: run the Go services and the console on the host with reload against them; one terminal, one log stream. Application images only in CI and releases. The compose file keeps an `app` profile for the end-to-end suite.
- Kubernetes: `make k8s-install` over the existing charts, a small-cluster values file, uninstall, and a guide.
- Memory: `GOMEMLIMIT` derived from the container limit, small pools, measured resident size per service (`make mem-report`) published in the docs with the budget.
- Edge: `ETag`/`Cache-Control` on cacheable reads (entitlements, plans), conditional requests, region header passthrough, hosted pages cacheable only when public data is safe (they are not: `no-store` stays).

## Exit criteria

A new clone reaches a running dev stack with two commands; the chart installs on kind with the small values; the memory report is in the repository and CI fails if a service exceeds its budget by 25 %.

## As built

- **`make setup` / `make dev`:** as described in [10-local-dev.md](../10-local-dev.md); `quotifyctl dev` reads the compose file, so there is no second list of settings. Tested: the address rewrite and the service selection (unit), and a real run of the gateway against the containers. The old `make dev` is `make dev-docker`.
- **Kubernetes:** `make k8s-install` / `k8s-uninstall` on the cluster `kubectl` points at, `values-small.yaml`, and `values-secrets.example.yaml` (one file for both charts). Rendered and linted by `make helm-lint` and the chart tests; **not installed on a cluster other than kind in CI**.
- **Memory:** every service sets the Go soft memory limit to 90 % of its container limit at start (`pkg/memlimit`, called from `config.Load`); `make mem-report` prints the resident memory of the running containers. The budget below is the chart's limit per service, not a measurement; CI enforces a measured budget per service since [Phase 33](phase-33-mail-memory-docs.md) (`deploy/compose/memory-budgets.yaml`).
- **Edge:** every successful `GET` carries a weak `ETag` and `Cache-Control: private, no-cache` and answers `If-None-Match` with `304`, `Vary: Authorization, X-Quotify-Tenant`; a published plan version (`/versions/{n}`) adds `max-age=300`. The hosted invoice page stays `no-store`. The gateway and the other services hold no state between requests.
