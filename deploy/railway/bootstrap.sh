#!/usr/bin/env bash
# Creates Quotify's services on Railway, sets their variables and deploys them in dependency order, with the Railway CLI.
# Idempotent: existing services, volumes and domains are skipped, variables are set to the same values again, and a generated secret is
# only generated when the variable does not exist yet. --dry-run prints every command and runs none (the CLI need not be installed).
# Read deploy/railway/README.md first; this script is its "Quick path". Variable values use Railway's reference syntax, ${{service.NAME}}.
#
#   deploy/railway/bootstrap.sh [--dry-run] [--profile full|small] [--demo] [--repo owner/name] [--branch main] [--source github|local] [--wait SECONDS]
#
#   --profile small  leaves out ClickHouse and analytics-svc (the console's analytics views stay empty); full is the default
#   --demo           QUOTIFY_DEMO=true: every tenant is served by the mock provider (never next to real provider accounts)
#   --source local   deploy this checkout with `railway up` instead of connecting the GitHub repo (--repo is then not needed)
set -euo pipefail

DRY_RUN=0 PROFILE=full DEMO=false REPO="" BRANCH=main SOURCE=github WAIT=45
while [[ $# -gt 0 ]]; do
  case $1 in
    --dry-run) DRY_RUN=1 ;;
    --profile) PROFILE=$2; shift ;;
    --demo) DEMO=true ;;
    --repo) REPO=$2; shift ;;
    --branch) BRANCH=$2; shift ;;
    --source) SOURCE=$2; shift ;;
    --wait) WAIT=$2; shift ;;
    -h | --help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
[[ $PROFILE == full || $PROFILE == small ]] || { echo "--profile must be full or small" >&2; exit 2; }
[[ $SOURCE == github || $SOURCE == local ]] || { echo "--source must be github or local" >&2; exit 2; }
[[ $SOURCE == local || -n $REPO || $DRY_RUN == 1 ]] || { echo "--repo owner/name is required with --source github" >&2; exit 2; }
REPO=${REPO:-owner/quotify}
# OPERATOR_EMAILS (environment, comma separated): the people who are platform operators once they sign in and verify the address; empty: nobody.

# --- helpers ---------------------------------------------------------------------------------------------------------------------------------
run() {
  if ((DRY_RUN)); then
    printf '+'
    for a in "$@"; do
      if [[ $a =~ ^[A-Za-z0-9_./:=@,+-]*$ ]]; then printf ' %s' "$a"; else printf " '%s'" "$a"; fi
    done
    printf '\n'
  else
    "$@"
  fi
}
note() { printf '\n== %s\n' "$*"; }

EXISTING_SERVICES="" EXISTING_VOLUMES=""
if ((!DRY_RUN)); then
  command -v railway >/dev/null || { echo "install the Railway CLI (https://docs.railway.com/cli) and run: railway login && railway link" >&2; exit 1; }
  railway whoami >/dev/null || { echo "not logged in: railway login" >&2; exit 1; }
  EXISTING_SERVICES=$(railway service list --json 2>/dev/null || true)
  EXISTING_VOLUMES=$(railway volume list --json 2>/dev/null || true)
fi
has_service() { [[ $EXISTING_SERVICES == *"\"name\": \"$1\""* || $EXISTING_SERVICES == *"\"name\":\"$1\""* ]]; }
has_volume() { [[ $EXISTING_VOLUMES == *"\"mountPath\": \"$1\""* || $EXISTING_VOLUMES == *"\"mountPath\":\"$1\""* ]]; }

# add_service <name>: an empty service (nothing deploys until a source is connected or `railway up` runs).
add_service() { has_service "$1" || run railway add --service "$1"; }
# add_database <postgres|redis> <service name Railway gives it>
add_database() { has_service "$2" || run railway add --database "$1"; }
# volume <service> <mount path>
volume() { has_volume "$2" || run railway volume add --service "$1" --mount-path "$2"; }
# vars <service> KEY=VALUE...: set variables without redeploying; the deploy comes later, in order.
vars() { local svc=$1; shift; run railway variable set --service "$svc" --skip-deploys "$@"; }
# secret <service> <KEY> <generator command...>: generate and set only when the variable does not exist yet.
secret() {
  local svc=$1 key=$2; shift 2
  if ((DRY_RUN)); then
    printf '+ [only if %s is unset on %s] railway variable set --service %s --skip-deploys %s=$(%s)\n' "$key" "$svc" "$svc" "$key" "$*"
  elif railway variable list --service "$svc" --kv 2>/dev/null | grep -q "^$key="; then
    echo "  $key exists on $svc, kept"
  else
    railway variable set --service "$svc" --skip-deploys "$key=$("$@")"
  fi
}
hex24() { openssl rand -hex 24; }
hex32() { openssl rand -hex 32; }
b64() { openssl rand -base64 32; }

# Private addresses. PORT is set on every service (below) so these are fixed, not whatever Railway picks.
PG='${{Postgres.RAILWAY_PRIVATE_DOMAIN}}'
KAFKA='redpanda.railway.internal:9092'
AMQP='amqp://quotify:${{rabbitmq.RABBITMQ_PASSWORD}}@rabbitmq.railway.internal:5672/'
grpc_addr() { printf 'dns:///%s.railway.internal:%s' "$1" "$2"; }
# dsn <role kind: app|system|migrator> <service short name>
dsn() { printf 'postgres://%s_%s:${{init-postgres.PG_%s_PASSWORD}}@%s:5432/%s_db' "$2" "$1" "$(tr a-z A-Z <<<"$1")" "$PG" "$2"; }
pg_vars() { # <service name> <db short name> <kinds...>
  local svc=$1 db=$2; shift 2
  local out=()
  for k in "$@"; do out+=("QUOTIFY_PG_$(tr a-z A-Z <<<"$k")_DSN=$(dsn "$k" "$db")"); done
  vars "$svc" "${out[@]}" QUOTIFY_MIGRATE_ON_START=true
}
S3_VARS=('S3_ENDPOINT=http://minio.railway.internal:9000' 'S3_PUBLIC_ENDPOINT=https://${{minio.RAILWAY_PUBLIC_DOMAIN}}'
  'S3_ACCESS_KEY=${{minio.MINIO_ROOT_USER}}' 'S3_SECRET_KEY=${{minio.MINIO_ROOT_PASSWORD}}' 'S3_FORCE_PATH_STYLE=true')
common() { # <service name> <port>
  vars "$1" QUOTIFY_ENV=railway "QUOTIFY_SERVICE_NAME=$1" "PORT=$2"
}
# Railway bills by resident memory and gives a container no cgroup limit for the Go runtime to read, so the soft limit is set here: the collector
# works before the heap grows past it. GOGC is 50 by default (pkg/memlimit). Raise GOMEMLIMIT on a service that logs "memory limit" pressure.
GO_MEMLIMIT=${GO_MEMLIMIT:-96MiB}
go_service() { # <service name> <dir> <port>
  vars "$1" "SERVICE_DIR=$2" RAILWAY_DOCKERFILE_PATH=deploy/railway/go.Dockerfile "GOMEMLIMIT=$GO_MEMLIMIT"
  common "$1" "$3"
}

INFRA=(rabbitmq redpanda minio mailpit)
APPS_FIRST=(plans-svc)
APPS_SECOND=(ingest-svc aggregator limits-svc assets-svc mockpsp)
APPS_THIRD=(billing-svc payments-worker invoice-worker notifier)
if [[ $PROFILE == full ]]; then INFRA+=(clickhouse); APPS_SECOND+=(analytics-svc); fi

# --- 1. managed databases and services -------------------------------------------------------------------------------------------------------
note "Postgres and Redis (Railway plugins)"
add_database postgres Postgres
add_database redis Redis

note "Services (empty until deployed)"
for s in "${INFRA[@]}" init-postgres init-redpanda init-minio "${APPS_FIRST[@]}" "${APPS_SECOND[@]}" "${APPS_THIRD[@]}" gateway admin-ui; do add_service "$s"; done

# --- 2. variables and volumes ----------------------------------------------------------------------------------------------------------------
note "Backing services"
secret init-postgres PG_MIGRATOR_PASSWORD hex24
secret init-postgres PG_APP_PASSWORD hex24
secret init-postgres PG_SYSTEM_PASSWORD hex24
vars init-postgres 'PGHOST=${{Postgres.RAILWAY_PRIVATE_DOMAIN}}' 'PGPORT=5432' 'PGUSER=${{Postgres.PGUSER}}' 'PGPASSWORD=${{Postgres.PGPASSWORD}}' 'PGDATABASE=${{Postgres.PGDATABASE}}'

secret rabbitmq RABBITMQ_PASSWORD hex24
vars rabbitmq RAILWAY_RUN_UID=0 PORT=15692 RAILWAY_DOCKERFILE_PATH=deploy/railway/images/rabbitmq/Dockerfile
volume rabbitmq /var/lib/rabbitmq

vars redpanda RAILWAY_RUN_UID=0 PORT=9644 RAILWAY_DOCKERFILE_PATH=deploy/railway/images/redpanda/Dockerfile
volume redpanda /var/lib/redpanda/data
vars init-redpanda "QUOTIFY_KAFKA_BROKERS=$KAFKA" RAILWAY_DOCKERFILE_PATH=deploy/railway/images/redpanda-init/Dockerfile

secret minio MINIO_ROOT_PASSWORD hex24
vars minio MINIO_ROOT_USER=quotify RAILWAY_RUN_UID=0 PORT=9000 RAILWAY_DOCKERFILE_PATH=deploy/railway/images/minio/Dockerfile
volume minio /data
vars init-minio S3_ENDPOINT=http://minio.railway.internal:9000 'S3_ACCESS_KEY=${{minio.MINIO_ROOT_USER}}' 'S3_SECRET_KEY=${{minio.MINIO_ROOT_PASSWORD}}' \
  RAILWAY_DOCKERFILE_PATH=deploy/railway/images/minio-init/Dockerfile
vars init-postgres RAILWAY_DOCKERFILE_PATH=deploy/railway/images/postgres-init/Dockerfile

vars mailpit PORT=8025 RAILWAY_DOCKERFILE_PATH=deploy/railway/images/mailpit/Dockerfile

if [[ $PROFILE == full ]]; then
  secret clickhouse CLICKHOUSE_PASSWORD hex24
  vars clickhouse CLICKHOUSE_DB=quotify CLICKHOUSE_USER=quotify CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 RAILWAY_RUN_UID=0 PORT=8123 \
    RAILWAY_DOCKERFILE_PATH=deploy/railway/images/clickhouse/Dockerfile
  volume clickhouse /var/lib/clickhouse
fi

note "Secrets shared by services"
secret gateway QUOTIFY_OPERATOR_TOKEN hex32
secret payments-worker QUOTIFY_SECRETS_KEY b64
secret plans-svc QUOTIFY_OTP_PEPPER hex32
secret payments-worker PAYMENTS_INTERNAL_TOKEN hex32

note "plans-svc"
go_service plans-svc services/plans-svc 9100
pg_vars plans-svc plans app system migrator
vars plans-svc 'QUOTIFY_REDIS_URL=${{Redis.REDIS_URL}}' "QUOTIFY_KAFKA_BROKERS=$KAFKA" 'QUOTIFY_SECRETS_KEY=${{payments-worker.QUOTIFY_SECRETS_KEY}}' \
  QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox,live 'QUOTIFY_APP_URL=https://${{admin-ui.RAILWAY_PUBLIC_DOMAIN}}'

note "ingest-svc"
go_service ingest-svc services/ingest-svc 9100
pg_vars ingest-svc ingest app system migrator
vars ingest-svc "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_KAFKA_BROKERS=$KAFKA"

note "aggregator"
go_service aggregator services/aggregator 9100
pg_vars aggregator aggregator app system migrator
vars aggregator "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_KAFKA_BROKERS=$KAFKA"

note "limits-svc"
go_service limits-svc services/limits-svc 9100
vars limits-svc 'QUOTIFY_REDIS_URL=${{Redis.REDIS_URL}}' "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" \
  "QUOTIFY_KAFKA_BROKERS=$KAFKA"

note "billing-svc"
go_service billing-svc services/billing-svc 9100
pg_vars billing-svc billing app system migrator
vars billing-svc "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_INGEST_ADDR=$(grpc_addr ingest-svc 7002)" \
  "QUOTIFY_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" "QUOTIFY_KAFKA_BROKERS=$KAFKA" "QUOTIFY_RABBITMQ_URL=$AMQP" S3_BUCKET=invoices "${S3_VARS[@]}"

note "assets-svc"
go_service assets-svc services/assets-svc 9100
pg_vars assets-svc assets app system migrator
vars assets-svc S3_BUCKET=assets "${S3_VARS[@]}"

if [[ $PROFILE == full ]]; then
  note "analytics-svc"
  go_service analytics-svc services/analytics-svc 9100
  vars analytics-svc CLICKHOUSE_ADDR=clickhouse.railway.internal:9000 CLICKHOUSE_DB=quotify CLICKHOUSE_USER=quotify \
    'CLICKHOUSE_PASSWORD=${{clickhouse.CLICKHOUSE_PASSWORD}}' "QUOTIFY_KAFKA_BROKERS=$KAFKA"
fi

note "mockpsp"
go_service mockpsp tools/mockpsp 8090
pg_vars mockpsp mockpsp app migrator

note "payments-worker"
go_service payments-worker services/payments-worker 9100
pg_vars payments-worker payments system migrator
vars payments-worker "QUOTIFY_RABBITMQ_URL=$AMQP" MOCKPSP_URL=http://mockpsp.railway.internal:8090 "QUOTIFY_DEMO=$DEMO" \
  "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_BILLING_ADDR=$(grpc_addr billing-svc 7005)"

note "invoice-worker"
go_service invoice-worker services/invoice-worker 9100
vars invoice-worker "QUOTIFY_RABBITMQ_URL=$AMQP" S3_BUCKET=invoices S3_ASSETS_BUCKET=assets "${S3_VARS[@]}"

note "notifier"
go_service notifier services/notifier 9100
pg_vars notifier notifier system migrator
vars notifier "QUOTIFY_RABBITMQ_URL=$AMQP" "QUOTIFY_KAFKA_BROKERS=$KAFKA" "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" SMTP_ADDR=mailpit.railway.internal:1025

note "gateway"
go_service gateway services/gateway 8000
GW_ANALYTICS=()
[[ $PROFILE == full ]] && GW_ANALYTICS=("QUOTIFY_ANALYTICS_ADDR=$(grpc_addr analytics-svc 7006)")
vars gateway "QUOTIFY_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "QUOTIFY_INGEST_ADDR=$(grpc_addr ingest-svc 7002)" \
  "QUOTIFY_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" "QUOTIFY_LIMITS_ADDR=$(grpc_addr limits-svc 7004)" \
  "QUOTIFY_BILLING_ADDR=$(grpc_addr billing-svc 7005)" "QUOTIFY_ASSETS_ADDR=$(grpc_addr assets-svc 7007)" ${GW_ANALYTICS[@]+"${GW_ANALYTICS[@]}"} \
  'QUOTIFY_REDIS_URL=${{Redis.REDIS_URL}}' QUOTIFY_ALLOWED_ENVIRONMENTS=sandbox,live \
  QUOTIFY_PAYMENTS_URL=http://payments-worker.railway.internal:8095 'QUOTIFY_PAYMENTS_INTERNAL_TOKEN=${{payments-worker.PAYMENTS_INTERNAL_TOKEN}}' \
  'QUOTIFY_PUBLIC_URL=https://${{gateway.RAILWAY_PUBLIC_DOMAIN}}' 'QUOTIFY_APP_URL=https://${{admin-ui.RAILWAY_PUBLIC_DOMAIN}}' "QUOTIFY_OPERATOR_EMAILS=${OPERATOR_EMAILS:-}"

note "admin-ui"
vars admin-ui RAILWAY_DOCKERFILE_PATH=deploy/railway/admin.Dockerfile PORT=3001 GATEWAY_URL=http://gateway.railway.internal:8000 "QUOTIFY_DEMO=$DEMO" BRAND_NAME=Payrudder

# --- 3. public domains ------------------------------------------------------------------------------------------------------------------------
note "Public domains (the gateway, the console, and MinIO for presigned links)"
for pair in gateway:8000 admin-ui:3001 minio:9000; do
  svc=${pair%%:*}
  if ((!DRY_RUN)) && railway domain list --service "$svc" --json 2>/dev/null | grep -q '"domain"'; then
    echo "  $svc has a domain, kept"
  else
    run railway domain --service "$svc" --port "${pair##*:}"
  fi
done

# --- 4. deploy in dependency order ------------------------------------------------------------------------------------------------------------
deploy() { # <service>...
  for s in "$@"; do
    if [[ $SOURCE == github ]]; then
      run railway service source connect --repo "$REPO" --branch "$BRANCH" --service "$s"
    else
      run railway up --service "$s" --detach
    fi
  done
}
pause() { echo "-- waiting ${WAIT}s: $*"; ((DRY_RUN)) || sleep "$WAIT"; }

note "Deploy: backing services"
deploy "${INFRA[@]}"
pause "the brokers and storage come up (Postgres and Redis are already running)"
note "Deploy: one-shot jobs (databases and roles, Kafka topics, buckets)"
deploy init-postgres init-redpanda init-minio
pause "the jobs finish (check that each shows Completed)"
note "Deploy: services, plans-svc first (everything else reads it)"
deploy "${APPS_FIRST[@]}"
pause "plans-svc migrates and starts"
deploy "${APPS_SECOND[@]}"
pause "ingest-svc, aggregator and the rest migrate and start"
deploy "${APPS_THIRD[@]}"
deploy gateway admin-ui

cat <<EOF

Done. Still manual (the CLI cannot set them):
  - each service's Settings > Config-as-code: /deploy/railway/services/<name>.railway.json (apps) or /deploy/railway/infra/<name>.railway.json (the rest)
    gives it the health check, restart policy and watch paths. Without it the build still works (RAILWAY_DOCKERFILE_PATH is set) but nothing is health-checked.
  - custom domains: railway domain <your.domain> --service gateway --port 8000 (and admin-ui, port 3001), then the DNS records it prints.
  - the first tenant: see "First tenant" in deploy/railway/README.md. The operator token: railway variable list --service gateway --kv
EOF
