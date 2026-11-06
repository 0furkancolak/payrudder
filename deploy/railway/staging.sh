#!/usr/bin/env bash
# Staging runs only while someone uses it: its services are stopped (the running deployment is removed; the volumes and the variables stay) and
# woken again by redeploying their last built deployment, which needs no build. Both go through Railway's API (staging_api.py): the CLI cannot
# redeploy a removed deployment, nor remove a sleeping one.
#
#   deploy/railway/staging.sh start [--detach]   wake every service in dependency order, wait until each runs and the API answers, print the links;
#                                                 then stay in the foreground and stop it all when you leave (Ctrl-C, closing the terminal).
#                                                 --detach returns once it is ready and leaves it running (deploy-staging.yml).
#   deploy/railway/staging.sh stop               stop every service
#   deploy/railway/staging.sh status             print running, stopped or partial
#
# make start-staging and make stop-staging call it. Needs python3 and the Railway CLI's login (or RAILWAY_TOKEN, a project token of staging). A nightly
# workflow (staging-stop.yml) stops staging too, for a session that ended without Ctrl-C (a laptop put to sleep).
set -euo pipefail

PROJECT=${RAILWAY_PROJECT_ID:-c43f5930-75ba-4cda-970f-57f13b2a9089}
ENV=${STAGING_ENVIRONMENT:-staging}
API=https://api-staging.payrudder.com
CONSOLE=https://console-staging.payrudder.com
# Woken in this order, each tier running before the next; stopped in reverse. A service of the environment in no tier is woken last.
TIERS=("Postgres Redis" "pgbouncer rabbitmq redpanda" "plans-svc"
  "ingest-svc aggregator limits-svc assets-svc mockpsp billing-svc payments-worker invoice-worker notifier" "gateway")
TIMEOUT=${STAGING_TIMEOUT_SECONDS:-900}

# states: "<name> <up|stopped|starting|failed>" for every service. Sleeping (Railway's serverless) counts as up: the next request wakes it.
api() { python3 "$(dirname "$0")/staging_api.py" "$PROJECT" "$ENV" "$@"; }
states() { api states; }

ordered() { # every service of the environment, tier by tier, the unknown ones last
  local all known=" ${TIERS[*]} " rest=()
  all=$(states | cut -d' ' -f1)
  for t in "${TIERS[@]}"; do for s in $t; do grep -qx "$s" <<<"$all" && echo "$s"; done; done
  for s in $all; do [[ $known == *" $s "* ]] || rest+=("$s"); done
  ((${#rest[@]})) && printf '%s\n' "${rest[@]}"
  return 0
}

status() {
  local st
  st=$(states | cut -d' ' -f2 | sort -u | tr '\n' ' ')
  case $st in
    "up ") echo running ;;
    "stopped ") echo stopped ;;
    *) echo partial ;;
  esac
}

stop() {
  echo "== staging: stopping"
  local s st
  st=$(states)
  for s in $(ordered | tail -r 2>/dev/null || ordered | tac); do
    grep -qx "$s stopped" <<<"$st" && continue
    api stop "$s" && echo "   $s stopped" || echo "   $s: could not stop" >&2
  done
}

# wait_up <service...>: until each is up, or a failure / the timeout stops the script.
wait_up() {
  local deadline=$((SECONDS + TIMEOUT)) st s pending
  while :; do
    st=$(states)
    pending=()
    for s in "$@"; do
      case $(grep "^$s " <<<"$st" | cut -d' ' -f2) in
        up) ;;
        failed) echo "staging: $s failed to start (railway logs --service $s --environment $ENV)" >&2; return 1 ;;
        *) pending+=("$s") ;;
      esac
    done
    ((${#pending[@]} == 0)) && return 0
    ((SECONDS < deadline)) || { echo "staging: still starting after ${TIMEOUT}s: ${pending[*]}" >&2; return 1; }
    sleep 5
  done
}

start() {
  echo "== staging: starting"
  local all tier s st woken
  all=$(ordered)
  st=$(states)
  for tier in "${TIERS[@]}" "$(comm -23 <(sort <<<"$all") <(tr ' ' '\n' <<<"${TIERS[*]}" | sort) | tr '\n' ' ')"; do
    woken=()
    for s in $tier; do
      grep -qx "$s" <<<"$all" || continue
      woken+=("$s")
      grep -qx "$s up" <<<"$st" && continue
      api wake "$s"
    done
    ((${#woken[@]})) || continue
    sleep 5 # the new deployment is listed a moment after the redeploy, until then the old (removed, failed) one is the newest
    wait_up "${woken[@]}"
    echo "   up: ${woken[*]}"
  done
  # The gateway may be asleep: the first request wakes it.
  local deadline=$((SECONDS + 120))
  until [[ $(curl -s -o /dev/null -w '%{http_code}' -m 30 "$API/healthz") == 200 ]]; do
    ((SECONDS < deadline)) || { echo "staging: $API/healthz does not answer" >&2; return 1; }
    sleep 3
  done
  cat <<EOF

Staging hazır.
  Console:  $CONSOLE
  API:      $API
  Railway:  https://railway.com/project/$PROJECT
EOF
}

case ${1:-} in
  start)
    if [[ ${2:-} == --detach ]]; then start; exit; fi
    trap 'trap - EXIT INT TERM HUP; echo; stop' EXIT INT TERM HUP
    start
    echo
    echo "Kapatmak için Ctrl-C: staging de kapanır."
    while :; do sleep 3600; done
    ;;
  stop) stop ;;
  status) status ;;
  *) sed -n '2,13p' "$0" >&2; exit 2 ;;
esac
