SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# Go modules in the workspace (go.work). tools/ is deliberately outside the workspace: its large dependency
# graph (golangci-lint, buf, sqlc) must not influence the versions selected for services.
MODULES := pkg gen/go services/plans-svc services/gateway tests tools/quotifyctl
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
gen: tools ## Generate code (buf, sqlc for every service with a sqlc.yaml)
	rm -rf gen/go/quotify
	$(BIN)/buf generate
	@for f in services/*/sqlc.yaml; do (cd $$(dirname $$f) && $(BIN)/sqlc generate); done
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

.PHONY: test-integration
test-integration: ## Integration tests (build tag integration; needs Docker)
	@for m in $(MODULES); do (cd $$m && go test -race -count=1 -tags integration ./...); done

.PHONY: e2e
e2e: ## End-to-end tests against the running compose stack (make up first)
	cd tests && go test -count=1 -tags e2e ./e2e/...

OPERATOR_TOKEN ?= local-operator-token-change-me-0123456789
.PHONY: seed
seed: ## Seed a demo account: make seed CURRENCY=TRY TIMEZONE=Europe/Istanbul GRACE=24h
	@test -n "$(CURRENCY)" -a -n "$(TIMEZONE)" -a -n "$(GRACE)" || { echo "usage: make seed CURRENCY=TRY TIMEZONE=Europe/Istanbul GRACE=24h"; exit 2; }
	cd tools/quotifyctl && go run . seed --operator-token "$(OPERATOR_TOKEN)" --currency "$(CURRENCY)" --timezone "$(TIMEZONE)" --grace "$(GRACE)"

.PHONY: up-infra
up-infra: ## Start infrastructure only (run services from your IDE)
	$(COMPOSE) up -d --wait
	$(COMPOSE) run --rm minio-init

.PHONY: up
up: ## Start infrastructure + observability + Quotify services
	$(COMPOSE) --profile obs --profile app up -d --build --wait
	$(COMPOSE) run --rm minio-init

.PHONY: down
down: ## Stop every profile (keeps volumes)
	$(COMPOSE) --profile obs --profile app --profile chaos --profile init down

.PHONY: clean
clean: ## Stop everything and delete volumes and ./bin
	$(COMPOSE) --profile obs --profile app --profile chaos --profile init down -v
	rm -rf $(BIN)
