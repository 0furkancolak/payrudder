#!/usr/bin/env bash
# NetworkPolicy check against a running cluster (make netpol-test): ingest-svc:7002 is reachable from a pod that carries the
# gateway's labels, and not from a pod in another namespace or from an unlabelled pod of the same namespace.
set -uo pipefail
ctx=${KUBE_CONTEXT:-kind-quotify}
ns=${HELM_NS:-quotify}
k="kubectl --context $ctx"
target="ingest-svc.$ns.svc.cluster.local"

probe() { # probe <namespace> <labels>: exit status of nc against ingest-svc:7002
  $k run "np-$RANDOM" -n "$1" --rm -i --restart=Never --quiet --image=busybox:1.37 --labels="$2" --command -- nc -z -w 3 "$target" 7002 >/dev/null 2>&1
}

$k create namespace netpol-outsider >/dev/null 2>&1 || true
fail=0

if probe "$ns" "app.kubernetes.io/name=gateway,app.kubernetes.io/instance=quotify"; then
  echo "ok    a pod with the gateway's labels reaches ingest-svc:7002"
else
  echo "FAIL  a pod with the gateway's labels cannot reach ingest-svc:7002"; fail=1
fi
if probe "$ns" "app=intruder"; then
  echo "FAIL  an unlabelled pod of the same namespace reaches ingest-svc:7002"; fail=1
else
  echo "ok    an unlabelled pod of the same namespace is refused"
fi
if probe netpol-outsider "app.kubernetes.io/name=gateway,app.kubernetes.io/instance=quotify"; then
  echo "FAIL  a pod in another namespace reaches ingest-svc:7002 (even with the gateway's labels)"; fail=1
else
  echo "ok    a pod in another namespace is refused"
fi
$k delete namespace netpol-outsider --wait=false >/dev/null 2>&1
exit $fail
