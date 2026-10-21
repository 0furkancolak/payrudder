SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# Go modules in the workspace (go.work). tools/ is deliberately outside the workspace: its large dependency
# graph (golangci-lint, buf, sqlc) must not influence the versions selected for services.
MODULES := pkg gen/go services/plans-svc services/ingest-svc services/aggregator services/limits-svc services/analytics-svc services/assets-svc services/billing-svc services/payments-worker services/invoice-worker services/notifier services/gateway tests tools/quotifyctl tools/mockpsp
BIN     := $(CURDIR)/bin
COMPOSE := docker compose -f deploy/compose/compose.yaml $(if $(wildcard .env),--env-file .env)
export GOPRIVATE := github.com/0furkancolak/*

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

$(BIN)/buf: tools/go.mod tools/go.sum
	cd tools && GOWORK=off go build -o $(BIN)/ tool
	@touch $(BIN)/buf

.PHONY: tools
tools: $(BIN)/buf ## Build pinned dev tools into ./bin (buf, golangci-lint, goose, sqlc, protoc plugins)

.PHONY: gen
gen: tools ## Generate code (buf, sqlc for every service and tool with a sqlc.yaml)
	rm -rf gen/go/quotify
	$(BIN)/buf generate
	@for f in services/*/sqlc.yaml tools/*/sqlc.yaml; do (cd $$(dirname $$f) && $(BIN)/sqlc generate); done
	@for m in $(MODULES); do (cd $$m && GOWORK=off go mod tidy); done

.PHONY: lint
lint: tools ## golangci-lint every module + buf lint
	$(BIN)/buf lint
	@for m in $(MODULES); do echo "lint $$m"; (cd $$m && $(BIN)/golangci-lint run ./...); done

.PHONY: fmt
fmt: tools ## Format Go code
	@for m in $(MODULES); do (cd $$m && $(BIN)/golangci-lint fmt ./...); done

.PHONY: test
test: ## Unit tests (race detector)
	@for m in $(MODULES); do (cd $$m && go test -race -count=1 ./...); done

.PHONY: test-rules
test-rules: ## Prometheus alert rules: promtool check + unit tests (Docker)
	docker run --rm -v "$(CURDIR)/deploy/prometheus:/p:ro" --entrypoint promtool prom/prometheus:v3.15.0 check rules /p/rules/quotify.yaml
	docker run --rm -v "$(CURDIR)/deploy/prometheus:/p:ro" --entrypoint promtool prom/prometheus:v3.15.0 test rules /p/tests/quotify_test.yaml

.PHONY: test-integration
test-integration: ## Integration tests (build tag integration; needs Docker)
	@for m in $(MODULES); do (cd $$m && go test -race -count=1 -tags integration ./...); done

.PHONY: e2e
e2e: ## End-to-end tests against the running compose stack (make up first)
	cd tests && go test -count=1 -tags e2e ./e2e/...

OPERATOR_TOKEN ?= local-operator-token-change-me-0123456789
CHAOS_COMPOSE := docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.chaos.yaml $(if $(wildcard .env),--env-file .env)
N ?= 100000
.PHONY: chaos
chaos: ## Correctness suite with chaos monkey: make chaos N=100000 (2 replicas behind Toxiproxy, chaos-tag fault hooks armed)
	$(MAKE) up-infra
	$(CHAOS_COMPOSE) --profile app --profile chaos up -d --build --wait
	cd tests && QUOTIFY_CHAOS_EVENTS=$(N) QUOTIFY_CHAOS_MONKEY=1 go test -count=1 -tags chaos -timeout 90m -v ./chaos/

# k6 runs on the compose network and writes its metrics to Prometheus (Grafana dashboard "Quotify / k6").
K6_IMAGE := grafana/k6:2.3.0
S ?= ingest_steady
.PHONY: load
load: ## k6 scenario against the running stack: make load S=ingest_steady (also: ingest_spike noisy_neighbor entitlement_reads month_close); K6_ENV="-e DURATION=2m -e EVENTS_PER_SEC=500"
	@mkdir -p tests/load/results
	docker run --rm --network quotify_default -v "$(CURDIR)/tests/load:/load" -e GATEWAY_URL=http://gateway:8000 \
		-e QUOTIFY_OPERATOR_TOKEN=$(OPERATOR_TOKEN) -e K6_PROMETHEUS_RW_SERVER_URL=http://prometheus:9090/api/v1/write \
		-e K6_PROMETHEUS_RW_TREND_STATS="p(95),p(99),avg,max" $(K6_ENV) $(K6_IMAGE) run -o experimental-prometheus-rw \
		--tag testid=$(S) --summary-export=/load/results/$(S).json /load/$(S).js

.PHONY: bench-idempotency
bench-idempotency: ## Partitioned vs unpartitioned idempotency table (needs the compose Postgres: make up-infra)
	cd tests && go test -count=1 -tags bench -run TestIdempotencyRetention -v -timeout 40m ./bench/

# --- Kubernetes (kind + Helm) ------------------------------------------------------------------
KIND_CONFIG ?= deploy/kind/cluster.yaml
# KIND_PROFILE: kind (3 workers, 2 replicas) or kind-min (one node, one replica)
KIND_PROFILE ?= kind
HELM_NS ?= quotify
KUBECTL := kubectl --context kind-quotify
SERVICE_IMAGES := plans-svc ingest-svc aggregator limits-svc billing-svc analytics-svc assets-svc gateway invoice-worker payments-worker notifier mockpsp

$(BIN)/helm $(BIN)/kubeconform $(BIN)/kind: tools/go.mod tools/go.sum
	cd tools && GOWORK=off go build -o $(BIN)/ helm.sh/helm/v3/cmd/helm github.com/yannh/kubeconform/cmd/kubeconform sigs.k8s.io/kind

.PHONY: rollout-test
rollout-test: ## helm upgrade to a new image tag under k6 load: no failed request (needs kind-images and helm-install first)
	KIND_PROFILE=$(KIND_PROFILE) HELM_NS=$(HELM_NS) deploy/kind/rollout-test.sh

.PHONY: demo-k8s
demo-k8s: ## The sandbox flow on kind: close a period, every pm_sandbox_* payment scenario plays out, PDFs in MinIO, receipts in Mailpit
	cd tests && QUOTIFY_KAFKA_BROKERS=localhost:31092 QUOTIFY_SKIP_OBSERVABILITY=1 go test -count=1 -tags e2e -timeout 30m -v -run 'TestSandboxBillingEndToEnd|TestSandboxPaymentScenarios|TestInvoiceLogoOnThePDF' ./e2e/

.PHONY: chaos-kind
chaos-kind: ## Correctness suite on kind with random pod deletion: make chaos-kind N=100000 (needs the 3-worker cluster and helm-install)
	cd tests && QUOTIFY_CHAOS_EVENTS=$(N) QUOTIFY_CHAOS_MONKEY=1 QUOTIFY_CHAOS_PLATFORM=kubernetes QUOTIFY_PG_HOST_PORT=15432 \
		QUOTIFY_KAFKA_BROKERS=localhost:31092$(comma)localhost:31093$(comma)localhost:31094 QUOTIFY_CLICKHOUSE_URL=http://localhost:8123 \
		go test -count=1 -tags chaos -timeout 120m -v ./chaos/

.PHONY: netpol-test
netpol-test: ## NetworkPolicy check on the running cluster (a pod in another namespace cannot reach ingest-svc:7002)
	deploy/kind/netpol-test.sh

.PHONY: helm-sync
helm-sync: ## Refresh the copies of shared files inside the infra chart (helm cannot read outside a chart)
	cp deploy/compose/redpanda/topics.sh deploy/helm/quotify-infra/files/topics.sh
	cp deploy/rabbitmq/definitions.json deploy/helm/quotify-infra/files/definitions.json
	cp deploy/clickhouse/low-memory.xml deploy/helm/quotify-infra/files/low-memory.xml

.PHONY: web-check
web-check: ## The operator console: install, lint, type check, build, and the OpenAPI types are up to date
	cd web/admin && npm ci --no-audit --no-fund && npm run lint && npm run typecheck && npm test && npm run gen && git diff --exit-code src/shared/api/schema.d.ts && npm run build

.PHONY: web-e2e
web-e2e: ## Playwright smoke test of the console against the running stack (make up first)
	cd web/admin && npx playwright install chromium && npm run e2e

.PHONY: kind-up
kind-up: $(BIN)/kind ## Create the kind cluster: make kind-up [KIND_CONFIG=deploy/kind/cluster-min.yaml]
	$(BIN)/kind get clusters 2>/dev/null | grep -qx quotify || $(BIN)/kind create cluster --config $(KIND_CONFIG) --wait 120s

.PHONY: kind-down
kind-down: $(BIN)/kind ## Delete the kind cluster
	$(BIN)/kind delete cluster --name quotify

.PHONY: kind-images
kind-images: ## Build the service images and load them into the kind nodes
	@# One at a time: twelve parallel Go builds exhaust a small Docker VM (and take the daemon down with them).
	@for i in $(SERVICE_IMAGES); do echo "build quotify/$$i:dev"; $(COMPOSE) --profile app build $$i; done
	@for i in $(SERVICE_IMAGES); do echo "load quotify/$$i:dev"; $(BIN)/kind load docker-image --name quotify quotify/$$i:dev; done

.PHONY: helm-install
helm-install: $(BIN)/helm ## Install (or upgrade) quotify-infra, then quotify: make helm-install [KIND_PROFILE=kind-min] [HELM_ARGS="--set ..."]
	$(BIN)/helm upgrade --install quotify-infra deploy/helm/quotify-infra -n $(HELM_NS) --create-namespace --kube-context kind-quotify --wait --timeout 10m \
		$(if $(filter kind,$(KIND_PROFILE)),-f deploy/helm/quotify-infra/values-ha.yaml) -f deploy/helm/quotify-infra/values-$(KIND_PROFILE).yaml
	$(BIN)/helm upgrade --install quotify deploy/helm/quotify -n $(HELM_NS) --kube-context kind-quotify --wait --timeout 10m \
		-f deploy/helm/quotify/values-$(KIND_PROFILE).yaml $(HELM_ARGS)

K8S_NS ?= quotify
REGISTRY ?=
TAG ?= dev
.PHONY: k8s-install
k8s-install: $(BIN)/helm ## Install on the cluster kubectl points at: make k8s-install REGISTRY=ghcr.io/you/ TAG=v0.5.0 SECRETS=my-secrets.yaml (see deploy/helm/quotify/values-secrets.example.yaml)
	@test -f "$(SECRETS)" || { echo "SECRETS=<file> is required: copy deploy/helm/quotify/values-secrets.example.yaml and change every value"; exit 2; }
	$(BIN)/helm upgrade --install quotify-infra deploy/helm/quotify-infra -n $(K8S_NS) --create-namespace --wait --timeout 10m -f $(SECRETS)
	$(BIN)/helm upgrade --install quotify deploy/helm/quotify -n $(K8S_NS) --wait --timeout 10m -f deploy/helm/quotify/values-small.yaml -f $(SECRETS) \
		--set global.imageRegistry=$(REGISTRY) --set global.imageTag=$(TAG) $(HELM_ARGS)
	@echo "Installed. Reach the gateway with: kubectl -n $(K8S_NS) port-forward svc/gateway 8000:8000"

.PHONY: k8s-uninstall
k8s-uninstall: $(BIN)/helm ## Remove both releases from the cluster kubectl points at (volumes stay until you delete the namespace)
	-$(BIN)/helm uninstall quotify -n $(K8S_NS)
	-$(BIN)/helm uninstall quotify-infra -n $(K8S_NS)

.PHONY: helm-uninstall
helm-uninstall: $(BIN)/helm ## Remove both releases
	-$(BIN)/helm uninstall quotify -n $(HELM_NS) --kube-context kind-quotify
	-$(BIN)/helm uninstall quotify-infra -n $(HELM_NS) --kube-context kind-quotify

.PHONY: helm-lint
helm-lint: $(BIN)/helm $(BIN)/kubeconform ## helm lint + kubeconform for every values file
	@set -e; for c in quotify quotify-infra; do \
	  for v in deploy/helm/$$c/values*.yaml; do \
	    echo "== $$c $$v"; \
	    $(BIN)/helm lint deploy/helm/$$c -f $$v; \
	    $(BIN)/helm template x deploy/helm/$$c -n quotify -f $$v | $(BIN)/kubeconform -strict -summary -kubernetes-version 1.33.0 -; \
	  done; \
	done

.PHONY: e2e-kind
e2e-kind: ## End-to-end tests against the kind cluster (make kind-up kind-images helm-install first)
	cd tests && QUOTIFY_KAFKA_BROKERS=localhost:31092$(if $(filter kind,$(KIND_PROFILE)),$(comma)localhost:31093$(comma)localhost:31094) \
		QUOTIFY_SKIP_OBSERVABILITY=1 go test -count=1 -tags e2e -timeout 40m ./e2e/...

comma := ,

.PHONY: demo
demo: ## The README story on a running stack: usage, a period close, a price change, re-rating (sandbox dunning on seconds)
	QUOTIFY_DUNNING_SANDBOX=15s,15s,15s QUOTIFY_SWEEP_INTERVAL=2s QUOTIFY_SCHEDULER_INTERVAL=5s $(COMPOSE) --profile obs --profile app up -d --wait billing-svc
	cd tools/quotifyctl && go run . demo --operator-token "$(OPERATOR_TOKEN)"

.PHONY: demo-mode
demo-mode: ## Demo mode: the whole stack with only the mock provider, a banner in the console, sample data and the README story
	QUOTIFY_DEMO=true QUOTIFY_DUNNING_SANDBOX=15s,15s,15s QUOTIFY_SWEEP_INTERVAL=2s QUOTIFY_SCHEDULER_INTERVAL=5s $(COMPOSE) --profile obs --profile app up -d --wait
	cd tools/quotifyctl && go run . demo --operator-token "$(OPERATOR_TOKEN)"
	@echo "console: http://localhost:3001   mail: http://localhost:8025"

.PHONY: seed
seed: ## Seed a demo account: make seed CURRENCY=TRY TIMEZONE=Europe/Istanbul GRACE=24h
	@test -n "$(CURRENCY)" -a -n "$(TIMEZONE)" -a -n "$(GRACE)" || { echo "usage: make seed CURRENCY=TRY TIMEZONE=Europe/Istanbul GRACE=24h"; exit 2; }
	cd tools/quotifyctl && go run . seed --operator-token "$(OPERATOR_TOKEN)" --currency "$(CURRENCY)" --timezone "$(TIMEZONE)" --grace "$(GRACE)"

.PHONY: topics
topics: ## Create the Kafka topics (idempotent; needs redpanda running)
	$(COMPOSE) exec -T redpanda sh -s < deploy/compose/redpanda/topics.sh

.PHONY: railway-check
railway-check: ## Railway deploy files: static tests, shell syntax, bootstrap dry runs (no account or Docker needed)
	cd tests && go test -count=1 ./railway/
	bash -n deploy/railway/bootstrap.sh
	deploy/railway/bootstrap.sh --dry-run > /dev/null
	deploy/railway/bootstrap.sh --dry-run --profile small --demo > /dev/null

.PHONY: up-infra
up-infra: ## Start the infrastructure containers only (make setup does this and more)
	$(COMPOSE) up -d --wait
	$(COMPOSE) run --rm minio-init
	$(MAKE) topics

.PHONY: up
up: ## Start infrastructure + observability + Quotify services
	$(MAKE) up-infra
	$(COMPOSE) --profile obs --profile app up -d --build --wait

.PHONY: setup
setup: tools ## Once per machine: build the tools, start the infrastructure containers (Postgres, Redpanda, RabbitMQ, Redis, MinIO, Mailpit…), install the console's packages
	$(MAKE) up-infra
	cd web/admin && ([ -d node_modules ] || npm ci --no-audit --no-fund)
	@echo "Ready. Run: make dev"

.PHONY: dev
dev: ## Every day: the services run on your machine with reload (against the containers from make setup), the console on http://localhost:3001. Ctrl-C stops all. make dev ONLY=gateway,plans-svc runs some.
	cd tools/quotifyctl && go build -o $(BIN)/quotifyctl .
	@trap 'kill 0' INT TERM EXIT; \
	  $(BIN)/quotifyctl dev $(if $(ONLY),--only $(ONLY)) $(if $(SKIP),--skip $(SKIP)) & \
	  (cd web/admin && exec npm run dev) & \
	  wait

.PHONY: dev-docker
dev-docker: ## The services in Docker too, with the console on http://localhost:3001 (what make dev used to do; for checking the images)
	$(MAKE) up-infra
	COMPOSE_PARALLEL_LIMIT=1 $(COMPOSE) --profile obs --profile app up -d --build --wait --scale admin-ui=0
	@echo "Stack is up. Logs: make logs  (one service: make logs S=gateway). Stop: make down."
	@echo "Console: http://localhost:3001, operator token and playground form are pre-filled."
	cd web/admin && ([ -d node_modules ] || npm ci --no-audit --no-fund) && exec npm run dev

.PHONY: mem-report
mem-report: ## Resident memory of every running Quotify container (the budget is in docs/phases/phase-27-dev-and-operations.md)
	@docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' $$(docker ps --filter name=quotify- --format '{{.Names}}' | sort)

.PHONY: logs
logs: ## Follow service logs: make logs, or make logs S=gateway
	$(COMPOSE) --profile obs --profile app logs -f --tail=100 $(S)

.PHONY: down
down: ## Stop every profile (keeps volumes)
	$(COMPOSE) --profile obs --profile app --profile chaos --profile init down

.PHONY: clean
clean: ## Stop everything and delete volumes and ./bin
	$(COMPOSE) --profile obs --profile app --profile chaos --profile init down -v
	rm -rf $(BIN)
