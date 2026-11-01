#!/bin/sh
# Checks the Alertmanager configs with amtool, in the image the stack runs (make test-rules): they parse, and every kind of alert goes to the receiver it should
# (a page to on-call, a warning to the team, the Watchdog heartbeat to its own receiver, an alert without a severity to the team). Needs Docker.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
amtool() { docker run --rm -v "$here:/a:ro" --entrypoint amtool prom/alertmanager:v0.28.1 "$@"; }

for f in alertmanager.yml alertmanager.webhook.yml alertmanager.receivers.example.yml; do
  amtool check-config "/a/$f" >/dev/null || { amtool check-config "/a/$f"; exit 1; }
done

# routed <config> <receivers it must go to> <labels...>
routed() {
  cfg="$1"; want="$2"; shift 2
  got="$(amtool config routes test --config.file="/a/$cfg" "$@")"
  [ "$got" = "$want" ] || { echo "FAIL: $cfg: $* goes to '$got', want '$want'" >&2; exit 1; }
}
routed alertmanager.yml page severity=page service=postgres alertname=BackupStale
routed alertmanager.yml warn severity=warn service=postgres alertname=RestoreNotVerified
routed alertmanager.yml watchdog severity=none alertname=Watchdog
routed alertmanager.yml warn alertname=NoSeverity
for f in alertmanager.webhook.yml alertmanager.receivers.example.yml; do
  routed "$f" on-call severity=page service=postgres alertname=BackupStale
  routed "$f" team severity=warn service=postgres alertname=RestoreNotVerified
  routed "$f" watchdog severity=none alertname=Watchdog
  routed "$f" team alertname=NoSeverity
done
echo "alertmanager: configs valid, routes as documented"
