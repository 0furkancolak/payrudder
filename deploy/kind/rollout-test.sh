#!/usr/bin/env bash
# Zero-downtime rollout check (make rollout-test): while k6 sends events through the gateway, helm upgrade moves every service
# to a new image tag. The test passes when k6 saw no failed request (no 5xx, no connection error, no 429).
set -euo pipefail
profile=${KIND_PROFILE:-kind}
ns=${HELM_NS:-quotify}
root=$(cd "$(dirname "$0")/../.." && pwd)
bin=$root/bin
images=(plans-svc ingest-svc aggregator limits-svc billing-svc analytics-svc assets-svc gateway invoice-worker payments-worker notifier mockpsp)
duration=${ROLLOUT_LOAD_SECONDS:-240}

for i in "${images[@]}"; do
  docker tag "quotify/$i:dev" "quotify/$i:rollout"
  "$bin/kind" load docker-image --name quotify "quotify/$i:rollout" >/dev/null
done

mkdir -p "$root/tests/load/results"
chmod a+rwx "$root/tests/load/results"   # k6 runs as an unprivileged user inside the container
docker rm -f quotify-rollout-k6 >/dev/null 2>&1 || true
rm -f "$root/tests/load/results/rollout.json"
# kind publishes the gateway on 127.0.0.1 only: on Linux the container shares the host network, on Docker Desktop it uses the host alias.
if [ "$(uname -s)" = Linux ]; then net=(--network host); gw=http://127.0.0.1:8000
else net=(--add-host host.docker.internal:host-gateway); gw=http://host.docker.internal:8000; fi
docker run -d --name quotify-rollout-k6 "${net[@]}" -v "$root/tests/load:/load" \
  -e GATEWAY_URL="$gw" -e QUOTIFY_OPERATOR_TOKEN="${OPERATOR_TOKEN:-local-operator-token-change-me-0123456789}" \
  -e DURATION="${duration}s" -e EVENTS_PER_SEC=500 -e TENANTS=3 \
  grafana/k6:2.3.0 run --summary-export=/load/results/rollout.json /load/ingest_steady.js >/dev/null

sleep 30   # setup() creates the tenants first; the load starts after that
echo "upgrading to the new image tag while the load runs"
"$bin/helm" upgrade quotify "$root/deploy/helm/quotify" -n "$ns" --kube-context kind-quotify --wait --timeout 10m \
  -f "$root/deploy/helm/quotify/values-$profile.yaml" --set global.imageTag=rollout

docker wait quotify-rollout-k6 >/dev/null 2>&1 || true
docker logs quotify-rollout-k6 2>&1 | tail -n 25 || true
docker rm -f quotify-rollout-k6 >/dev/null 2>&1 || true
python3 - "$root/tests/load/results/rollout.json" "${ROLLOUT_MAX_FAIL_RATE:-0}" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))["metrics"]
failed = m["http_req_failed"]["passes"]
total = m["http_reqs"]["count"]
limit = float(sys.argv[2])
print(f"{total} requests during the rollout, {failed} failed (allowed rate {limit})")
sys.exit(1 if failed > limit * total else 0)
PY
