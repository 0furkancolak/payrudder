# Phase 27 — Development, Kubernetes, memory and the edge

## Scope

- `make setup`: install tools, start the infrastructure containers only. `make dev`: run the Go services and the console on the host with reload against them; one terminal, one log stream. Application images only in CI and releases. The compose file keeps an `app` profile for the end-to-end suite.
- Kubernetes: `make k8s-install` over the existing charts, a small-cluster values file, uninstall, and a guide.
- Memory: `GOMEMLIMIT` derived from the container limit, small pools, measured resident size per service (`make mem-report`) published in the docs with the budget.
- Edge: `ETag`/`Cache-Control` on cacheable reads (entitlements, plans), conditional requests, region header passthrough, hosted pages cacheable only when public data is safe (they are not: `no-store` stays).

## Exit criteria

A new clone reaches a running dev stack with two commands; the chart installs on kind with the small values; the memory report is in the repository and CI fails if a service exceeds its budget by 25 %.
