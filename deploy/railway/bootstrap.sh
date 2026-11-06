#!/usr/bin/env bash
# Brings one environment of Payrudder's Railway project up to date with the Railway CLI: the resources from deploy/railway/iac/.railway/railway.ts
# (services, databases, volumes, canvas groups, health checks), then every service's variables, then the deploys in dependency order.
# Idempotent: the IaC apply changes only what differs, variables are set to the same values again, and a generated secret is only generated
# when the variable does not exist yet. --dry-run prints every command and runs none (the CLI need not be installed).
# Read deploy/railway/README.md first. Variable values use Railway's reference syntax, ${{service.NAME}}.
#
#   deploy/railway/bootstrap.sh --environment staging|production --console-url https://HOST [--api-domain HOST] [--allowed-environments LIST]
#                               [--demo] [--wait SECONDS] [--allow-destroy] [--dry-run]
#   deploy/railway/bootstrap.sh --deploy-only [--dry-run]
#
#   --environment    the Railway environment (it must exist). production gets ClickHouse and analytics-svc and is not connected to GitHub
#                    (a published release deploys it); any other environment builds main from GitHub (railway.ts)
#   --console-url    the console's address (the Cloudflare Worker, ADR 0021): links and sign-in go there
#   --api-domain     the gateway's custom domain (api.payrudder.com); created with `railway domain` the first time, which prints its DNS records
#   --allowed-environments  sandbox,live (default) or sandbox: a sandbox-only deployment refuses live accounts
#   --demo           PAYRUDDER_DEMO=true: every tenant is served by the mock provider (never next to real provider accounts)
#   --allow-destroy  let the IaC apply delete resources that railway.ts no longer has (it stops and says so otherwise)
#   --deploy-only    only `railway up --ci` of the Payrudder services of this checkout, in order (a release to production: deploy-production.yml).
#                    Creates and sets nothing; RAILWAY_TOKEN is a project token of one environment, so it needs no login and no --environment
set -euo pipefail

DRY_RUN=0 DEMO=false WAIT=45 DEPLOY_ONLY=0 ALLOW_DESTROY=0
ENVIRONMENT="" CONSOLE_URL="" API_DOMAIN="" ALLOWED_ENVS=sandbox,live
while [[ $# -gt 0 ]]; do
  case $1 in
    --dry-run) DRY_RUN=1 ;;
    --deploy-only) DEPLOY_ONLY=1 ;;
    --allow-destroy) ALLOW_DESTROY=1 ;;
    --demo) DEMO=true ;;
    --wait) WAIT=$2; shift ;;
    --environment) ENVIRONMENT=$2; shift ;;
    --console-url) CONSOLE_URL=$2; shift ;;
    --api-domain) API_DOMAIN=$2; shift ;;
    --allowed-environments) ALLOWED_ENVS=$2; shift ;;
    -h | --help) sed -n '2,21p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
[[ -n $ENVIRONMENT ]] || { echo "--environment is required (staging or production)" >&2; exit 2; }
if ((!DEPLOY_ONLY)); then
  [[ $CONSOLE_URL == https://* ]] || { echo "--console-url https://HOST is required" >&2; exit 2; }
fi
[[ $ALLOWED_ENVS == sandbox,live || $ALLOWED_ENVS == sandbox ]] || { echo "--allowed-environments must be sandbox,live or sandbox" >&2; exit 2; }
[[ $API_DOMAIN != *://* && $API_DOMAIN != */* ]] || { echo "--api-domain takes a host name, not a URL: $API_DOMAIN" >&2; exit 2; }
# Inputs from the environment (deploy/railway/setup-secrets.sh collects them into a file, with where each one comes from):
# NAFRU_BASE_URL, NAFRU_API_KEY (both or neither): the platform's mail goes through Nafru Email; set on the notifier. There is no mail catcher:
#   sandbox tenants' mail goes through Nafru too, with a marked subject. Empty: the notifier sends no mail (signup verification, invoices).
# S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY (required): object storage, Cloudflare R2 (https://<account id>.r2.cloudflarestorage.com and an R2 API
#   token's keys). S3_INVOICES_BUCKET and S3_ASSETS_BUCKET default to payrudder-invoices-<environment> and payrudder-assets-<environment>;
#   S3_REGION to auto. Presigned links use the same endpoint; the assets bucket also has a public domain (assets.payrudder.com) of its own.
# GEO_API_KEY: this environment's nafru-geo key, set as PAYRUDDER_GEO_API_KEY on the gateway (docs/geo-data.md). Empty: the address lists answer 503.
# OPERATOR_EMAILS, OPERATOR_SUPPORT_EMAILS (comma separated): the platform operators, and the read-only support operators.
# ERROR_TRACKING_URL: a GlitchTip or Sentry DSN, set as PAYRUDDER_ERROR_TRACKING_URL on every Go service. Empty: error tracking stays off.
# MAIL_WEBHOOK_SECRET: the "whsec_..." secret of Nafru's delivery-events webhook (https://<api domain>/inbound/mail-events), on the gateway.
# PAYRUDDER_GOOGLE_CLIENT_ID, PAYRUDDER_GOOGLE_CLIENT_SECRET: set on the gateway as they are, for "Continue with Google". Empty: not set.
if ((!DEPLOY_ONLY && !DRY_RUN)); then
  for v in S3_ENDPOINT S3_ACCESS_KEY S3_SECRET_KEY; do
    [[ -n ${!v:-} ]] || { echo "$v is required (deploy/railway/setup-secrets.sh $ENVIRONMENT)" >&2; exit 2; }
  done
fi
NAFRU_VARS=()
if [[ -n ${NAFRU_BASE_URL:-} && -n ${NAFRU_API_KEY:-} ]]; then
  NAFRU_VARS=("NAFRU_BASE_URL=$NAFRU_BASE_URL" "NAFRU_API_KEY=$NAFRU_API_KEY")
elif [[ -n ${NAFRU_BASE_URL:-}${NAFRU_API_KEY:-} ]]; then
  echo "NAFRU_BASE_URL and NAFRU_API_KEY go together: set both, or neither" >&2; exit 2
else
  echo "warning: no NAFRU_BASE_URL/NAFRU_API_KEY, the notifier sends no mail" >&2
fi
S3_ENDPOINT=${S3_ENDPOINT:-https://account.r2.cloudflarestorage.com} S3_ACCESS_KEY=${S3_ACCESS_KEY:-s3-access} S3_SECRET_KEY=${S3_SECRET_KEY:-s3-secret}
S3_INVOICES_BUCKET=${S3_INVOICES_BUCKET:-payrudder-invoices-$ENVIRONMENT} S3_ASSETS_BUCKET=${S3_ASSETS_BUCKET:-payrudder-assets-$ENVIRONMENT}
FULL=0
[[ $ENVIRONMENT == production ]] && FULL=1

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

if ((!DRY_RUN && !DEPLOY_ONLY)); then
  command -v railway >/dev/null || { echo "install the Railway CLI (https://docs.railway.com/cli) and run: railway login && railway link" >&2; exit 1; }
  railway whoami >/dev/null || { echo "not logged in: railway login" >&2; exit 1; }
  railway environment "$ENVIRONMENT"
fi
# vars <service> KEY=VALUE...: set variables without redeploying; the deploy comes later, in order.
vars() { local svc=$1; shift; run railway variable set --service "$svc" --skip-deploys "$@"; }
# secret <service> <KEY> <generator command...>: generate and set only when the variable does not exist yet.
secret() {
  local svc=$1 key=$2 existing; shift 2
  if ((DRY_RUN)); then
    printf '+ [only if %s is unset on %s] railway variable set --service %s --skip-deploys %s=$(%s)\n' "$key" "$svc" "$svc" "$key" "$*"
  # Read into a variable, not piped into grep -q: the CLI panics on the closed pipe, the check then says "unset" and a kept secret is replaced.
  # A failed read stops the script for the same reason (a new PAYRUDDER_SECRETS_KEY loses every sealed credential).
  elif ! existing=$(railway variable list --service "$svc" --kv 2>/dev/null); then
    echo "cannot read the variables of $svc; stopping before $key is replaced" >&2; exit 1
  elif grep -q "^$key=" <<<"$existing"; then
    echo "  $key exists on $svc, kept"
  else
    railway variable set --service "$svc" --skip-deploys "$key=$("$@")"
  fi
}
hex24() { openssl rand -hex 24; }
hex32() { openssl rand -hex 32; }
b64() { openssl rand -base64 32; }

# Private addresses. PORT is set on every service (below) so these are fixed, not whatever Railway picks.
KAFKA='redpanda.railway.internal:9092'
AMQP='amqp://payrudder:${{rabbitmq.RABBITMQ_PASSWORD}}@rabbitmq.railway.internal:5672/'
grpc_addr() { printf 'dns:///%s.railway.internal:%s' "$1" "$2"; }
# dsn <role kind: app|system|migrator> <service short name>. Only the app role goes through PgBouncer (transaction pooling): the system role
# LISTENs and takes session advisory locks, the migrator takes goose's session lock (docs/runbooks/scaling.md).
dsn() {
  local host='${{Postgres.RAILWAY_PRIVATE_DOMAIN}}:5432'
  [[ $1 == app ]] && host=pgbouncer.railway.internal:6432
  printf 'postgres://%s_%s:${{Postgres.PG_%s_PASSWORD}}@%s/%s_db' "$2" "$1" "$(tr a-z A-Z <<<"$1")" "$host" "$2"
}
# pg_vars <service name> <db short name> <kinds...>: the DSNs, and the admin DSN with which the service creates its database and roles at start
# (pgxx.Provision; there is no init job).
pg_vars() {
  local svc=$1 db=$2; shift 2
  local out=()
  for k in "$@"; do out+=("PAYRUDDER_PG_$(tr a-z A-Z <<<"$k")_DSN=$(dsn "$k" "$db")"); done
  vars "$svc" "${out[@]}" PAYRUDDER_MIGRATE_ON_START=true 'PAYRUDDER_PG_ADMIN_DSN=${{Postgres.DATABASE_URL}}'
}
API_URL='https://${{gateway.RAILWAY_PUBLIC_DOMAIN}}' APP_URL=$CONSOLE_URL
[[ -n $API_DOMAIN ]] && API_URL=https://$API_DOMAIN
S3_VARS=("S3_ENDPOINT=$S3_ENDPOINT" "S3_ACCESS_KEY=$S3_ACCESS_KEY" "S3_SECRET_KEY=$S3_SECRET_KEY" "S3_REGION=${S3_REGION:-auto}")
common() { # <service name> <port>
  vars "$1" PAYRUDDER_ENV=railway "PAYRUDDER_SERVICE_NAME=$1" "PORT=$2"
}
# Railway bills by resident memory and gives a container no cgroup limit for the Go runtime to read, so the soft limit is set here: the collector
# works before the heap grows past it. GOGC is 50 by default (pkg/memlimit). Raise GOMEMLIMIT on a service that logs "memory limit" pressure.
GO_MEMLIMIT=${GO_MEMLIMIT:-96MiB}
go_service() { # <service name> <dir> <port>
  vars "$1" "SERVICE_DIR=$2" "GOMEMLIMIT=$GO_MEMLIMIT"
  common "$1" "$3"
  if [[ -n ${ERROR_TRACKING_URL:-} && $1 != mockpsp ]]; then vars "$1" "PAYRUDDER_ERROR_TRACKING_URL=$ERROR_TRACKING_URL"; fi
}

INFRA=(pgbouncer rabbitmq redpanda)
APPS_FIRST=(plans-svc)
APPS_SECOND=(ingest-svc aggregator limits-svc assets-svc mockpsp)
APPS_THIRD=(billing-svc payments-worker invoice-worker notifier)
if ((FULL)); then INFRA+=(clickhouse); APPS_SECOND+=(analytics-svc); fi

if ((DEPLOY_ONLY)); then
  # shortcut: the backing services are not redeployed by a release (their images rarely change); deploy one by hand when it did.
  note "Deploy this checkout: plans-svc first, the gateway last; each waits for its build and stops the script when it fails"
  for s in "${APPS_FIRST[@]}" "${APPS_SECOND[@]}" "${APPS_THIRD[@]}" gateway; do run railway up --service "$s" --ci; done
  exit 0
fi

# --- 1. resources ----------------------------------------------------------------------------------------------------------------------------
note "Resources: deploy/railway/iac/.railway/railway.ts"
IAC=$(cd "$(dirname "$0")/iac" && pwd)
APPLY=(railway config apply --environment "$ENVIRONMENT" --yes)
((ALLOW_DESTROY)) && APPLY+=(--confirm-destructive)
# The gateway's custom domain: IaC keeps one that exists but cannot create it, so it is passed only once it does.
HAS_DOMAIN=0
if [[ -n $API_DOMAIN ]] && ((!DRY_RUN)) && grep -q "\"domain\": *\"$API_DOMAIN\"" <<<"$(railway domain list --service gateway --json 2>/dev/null)"; then HAS_DOMAIN=1; fi
if ((DRY_RUN)); then
  echo "+ (cd $IAC && npm ci && ${API_DOMAIN:+[PAYRUDDER_API_DOMAIN=$API_DOMAIN once it exists] }${APPLY[*]})"
else
  (cd "$IAC" && npm ci --silent --no-audit --no-fund && if ((HAS_DOMAIN)); then PAYRUDDER_API_DOMAIN=$API_DOMAIN "${APPLY[@]}"; else "${APPLY[@]}"; fi)
fi
if [[ -n $API_DOMAIN ]] && ((!HAS_DOMAIN)); then
  note "The gateway's domain: add the DNS records it prints in Cloudflare, DNS only (grey cloud)"
  run railway domain "$API_DOMAIN" --service gateway --port 8000
fi

# --- 2. variables ----------------------------------------------------------------------------------------------------------------------------
note "Backing services"
# The role passwords of every service's database (pgxx.Provision sets them); kept on the Postgres service, which the DSNs reference.
secret Postgres PG_MIGRATOR_PASSWORD hex24
secret Postgres PG_APP_PASSWORD hex24
secret Postgres PG_SYSTEM_PASSWORD hex24
vars pgbouncer 'PGBOUNCER_AUTH_PASSWORD=${{Postgres.PGPASSWORD}}' PORT=6432

secret rabbitmq RABBITMQ_PASSWORD hex24
vars rabbitmq RAILWAY_RUN_UID=0 PORT=15692
vars redpanda RAILWAY_RUN_UID=0 PORT=9644

if ((FULL)); then
  secret clickhouse CLICKHOUSE_PASSWORD hex24
  vars clickhouse CLICKHOUSE_DB=payrudder CLICKHOUSE_USER=payrudder CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 RAILWAY_RUN_UID=0 PORT=8123
fi

note "Secrets shared by services"
secret gateway PAYRUDDER_OPERATOR_TOKEN hex32
secret payments-worker PAYRUDDER_SECRETS_KEY b64
# Replacing this key: PAYRUDDER_SECRETS_KEY_ID / _PREVIOUS / _PREVIOUS_ID on payments-worker, and the same as references on plans-svc (README, docs/runbooks/secrets-key-rotation.md).
secret plans-svc PAYRUDDER_OTP_PEPPER hex32
secret payments-worker PAYMENTS_INTERNAL_TOKEN hex32
secret notifier NOTIFIER_MAIL_EVENTS_SECRET hex32
# The console's Worker proves itself to the gateway with this; the same value goes on the Worker as EDGE_AUTH_SECRET (docs/runbooks/console-first-deploy.md).
secret gateway PAYRUDDER_TRUSTED_PROXY_SECRET hex32

note "plans-svc"
go_service plans-svc services/plans-svc 9100
pg_vars plans-svc plans app system migrator
vars plans-svc 'PAYRUDDER_REDIS_URL=${{Redis.REDIS_URL}}' "PAYRUDDER_KAFKA_BROKERS=$KAFKA" PAYRUDDER_KAFKA_CREATE_TOPICS=true \
  'PAYRUDDER_SECRETS_KEY=${{payments-worker.PAYRUDDER_SECRETS_KEY}}' \
  "PAYRUDDER_ALLOWED_ENVIRONMENTS=$ALLOWED_ENVS" "PAYRUDDER_APP_URL=$APP_URL" "PAYRUDDER_OPERATOR_EMAILS=${OPERATOR_EMAILS:-}" "PAYRUDDER_OPERATOR_SUPPORT_EMAILS=${OPERATOR_SUPPORT_EMAILS:-}" \
  "PAYRUDDER_INGEST_ADDR=$(grpc_addr ingest-svc 7002)" "PAYRUDDER_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" "PAYRUDDER_BILLING_ADDR=$(grpc_addr billing-svc 7005)" \
  PAYRUDDER_PAYMENTS_URL=http://payments-worker.railway.internal:8095 'PAYRUDDER_PAYMENTS_INTERNAL_TOKEN=${{payments-worker.PAYMENTS_INTERNAL_TOKEN}}'
# A sandbox reset also empties analytics-svc where there is one.
if ((FULL)); then vars plans-svc "PAYRUDDER_ANALYTICS_ADDR=$(grpc_addr analytics-svc 7006)"; fi

note "ingest-svc"
go_service ingest-svc services/ingest-svc 9100
pg_vars ingest-svc ingest app system migrator
vars ingest-svc "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_KAFKA_BROKERS=$KAFKA"

note "aggregator"
go_service aggregator services/aggregator 9100
pg_vars aggregator aggregator app system migrator
vars aggregator "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_KAFKA_BROKERS=$KAFKA"

note "limits-svc"
go_service limits-svc services/limits-svc 9100
vars limits-svc 'PAYRUDDER_REDIS_URL=${{Redis.REDIS_URL}}' "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" \
  "PAYRUDDER_KAFKA_BROKERS=$KAFKA"

note "billing-svc"
go_service billing-svc services/billing-svc 9100
pg_vars billing-svc billing app system migrator
vars billing-svc "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_INGEST_ADDR=$(grpc_addr ingest-svc 7002)" \
  "PAYRUDDER_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" "PAYRUDDER_KAFKA_BROKERS=$KAFKA" "PAYRUDDER_RABBITMQ_URL=$AMQP" "S3_BUCKET=$S3_INVOICES_BUCKET" \
  "${S3_VARS[@]}" "PAYRUDDER_PUBLIC_URL=$API_URL"

note "assets-svc"
go_service assets-svc services/assets-svc 9100
pg_vars assets-svc assets app system migrator
vars assets-svc "S3_BUCKET=$S3_ASSETS_BUCKET" "${S3_VARS[@]}"

if ((FULL)); then
  note "analytics-svc"
  go_service analytics-svc services/analytics-svc 9100
  vars analytics-svc CLICKHOUSE_ADDR=clickhouse.railway.internal:9000 CLICKHOUSE_DB=payrudder CLICKHOUSE_USER=payrudder \
    'CLICKHOUSE_PASSWORD=${{clickhouse.CLICKHOUSE_PASSWORD}}' "PAYRUDDER_KAFKA_BROKERS=$KAFKA"
fi

note "mockpsp"
go_service mockpsp tools/mockpsp 8090
pg_vars mockpsp mockpsp app migrator

note "payments-worker"
go_service payments-worker services/payments-worker 9100
pg_vars payments-worker payments system migrator
vars payments-worker "PAYRUDDER_RABBITMQ_URL=$AMQP" 'PAYRUDDER_REDIS_URL=${{Redis.REDIS_URL}}' MOCKPSP_URL=http://mockpsp.railway.internal:8090 "PAYRUDDER_DEMO=$DEMO" \
  "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_BILLING_ADDR=$(grpc_addr billing-svc 7005)"

note "invoice-worker"
go_service invoice-worker services/invoice-worker 9100
vars invoice-worker "PAYRUDDER_RABBITMQ_URL=$AMQP" "S3_BUCKET=$S3_INVOICES_BUCKET" "S3_ASSETS_BUCKET=$S3_ASSETS_BUCKET" "${S3_VARS[@]}"

note "notifier"
go_service notifier services/notifier 9100
pg_vars notifier notifier system migrator
vars notifier 'PAYRUDDER_REDIS_URL=${{Redis.REDIS_URL}}' "PAYRUDDER_RABBITMQ_URL=$AMQP" "PAYRUDDER_KAFKA_BROKERS=$KAFKA" "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" \
  ${NAFRU_VARS[@]+"${NAFRU_VARS[@]}"} NOTIFIER_EVENTS_ADDR=:9111

note "gateway"
go_service gateway services/gateway 8000
GW_ANALYTICS=()
((FULL)) && GW_ANALYTICS=("PAYRUDDER_ANALYTICS_ADDR=$(grpc_addr analytics-svc 7006)")
vars gateway "PAYRUDDER_PLANS_ADDR=$(grpc_addr plans-svc 7001)" "PAYRUDDER_INGEST_ADDR=$(grpc_addr ingest-svc 7002)" \
  "PAYRUDDER_AGGREGATOR_ADDR=$(grpc_addr aggregator 7003)" "PAYRUDDER_LIMITS_ADDR=$(grpc_addr limits-svc 7004)" \
  "PAYRUDDER_BILLING_ADDR=$(grpc_addr billing-svc 7005)" "PAYRUDDER_ASSETS_ADDR=$(grpc_addr assets-svc 7007)" ${GW_ANALYTICS[@]+"${GW_ANALYTICS[@]}"} \
  'PAYRUDDER_REDIS_URL=${{Redis.REDIS_URL}}' "PAYRUDDER_ALLOWED_ENVIRONMENTS=$ALLOWED_ENVS" \
  PAYRUDDER_PAYMENTS_URL=http://payments-worker.railway.internal:8095 'PAYRUDDER_PAYMENTS_INTERNAL_TOKEN=${{payments-worker.PAYMENTS_INTERNAL_TOKEN}}' \
  "PAYRUDDER_PUBLIC_URL=$API_URL" "PAYRUDDER_APP_URL=$APP_URL" "PAYRUDDER_OPERATOR_EMAILS=${OPERATOR_EMAILS:-}" "PAYRUDDER_OPERATOR_SUPPORT_EMAILS=${OPERATOR_SUPPORT_EMAILS:-}" \
  PAYRUDDER_NOTIFIER_EVENTS_URL=http://notifier.railway.internal:9111 'PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET=${{notifier.NOTIFIER_MAIL_EVENTS_SECRET}}'
if [[ -n ${MAIL_WEBHOOK_SECRET:-} ]]; then vars gateway "PAYRUDDER_MAIL_WEBHOOK_SECRET=$MAIL_WEBHOOK_SECRET"; fi
if [[ -n ${GEO_API_KEY:-} ]]; then vars gateway "PAYRUDDER_GEO_API_KEY=$GEO_API_KEY"; fi
if [[ -n ${PAYRUDDER_GOOGLE_CLIENT_ID:-} ]]; then vars gateway "PAYRUDDER_GOOGLE_CLIENT_ID=$PAYRUDDER_GOOGLE_CLIENT_ID" "PAYRUDDER_GOOGLE_CLIENT_SECRET=${PAYRUDDER_GOOGLE_CLIENT_SECRET:-}"; fi

# --- 3. deploy in dependency order ------------------------------------------------------------------------------------------------------------
# No environment is connected to GitHub: this checkout is uploaded (later pushes and releases go through deploy-staging.yml / deploy-production.yml).
deploy() { # <service>...
  for s in "$@"; do run railway up --service "$s" --detach; done
}
pause() { echo "-- waiting ${WAIT}s: $*"; ((DRY_RUN)) || sleep "$WAIT"; }

note "Deploy: backing services"
deploy "${INFRA[@]}"
pause "the pooler and the brokers come up (Postgres and Redis are already running)"
note "Deploy: services, plans-svc first (it creates the Kafka topics; every service creates its own database)"
deploy "${APPS_FIRST[@]}"
pause "plans-svc migrates and starts"
deploy "${APPS_SECOND[@]}"
pause "ingest-svc, aggregator and the rest migrate and start"
deploy "${APPS_THIRD[@]}"
deploy gateway

cat <<EOF

Done. Still manual:
  - put the gateway's PAYRUDDER_TRUSTED_PROXY_SECRET on the console Worker as EDGE_AUTH_SECRET (docs/runbooks/staging-and-production.md).
  - the first tenant: see "First tenant" in deploy/railway/README.md. The operator token: railway variable list --service gateway --kv
EOF
