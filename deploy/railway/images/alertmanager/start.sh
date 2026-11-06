#!/bin/sh
# Writes the config from the environment and starts Alertmanager. TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID (both or neither): page and warn go
# to that chat. Without them alerts are kept in Alertmanager and Grafana only. WATCHDOG_URL (optional): the always-firing Watchdog is posted
# there every minute, for an outside dead man's switch (healthchecks.io and the like); without it the heartbeat goes nowhere.
set -eu
dir=/tmp/alertmanager
mkdir -p "$dir"
env=${RAILWAY_ENVIRONMENT_NAME:-unknown}

receiver() { # <name>
  echo "  - name: $1"
  [ -n "${TELEGRAM_BOT_TOKEN:-}" ] || return 0
  cat <<YAML
    telegram_configs:
      - bot_token_file: $dir/telegram_token
        chat_id: $TELEGRAM_CHAT_ID
        send_resolved: true
        parse_mode: HTML
        message: |
          {{ if eq .Status "firing" }}🔥{{ else }}✅{{ end }} <b>[$env] $1: {{ .CommonLabels.alertname }}</b>{{ with .CommonLabels.service }} ({{ . }}){{ end }}
          {{ range .Alerts }}• {{ .Annotations.summary }}
          {{ end }}
YAML
}

if [ -n "${TELEGRAM_BOT_TOKEN:-}" ]; then
  : "${TELEGRAM_CHAT_ID:?TELEGRAM_CHAT_ID goes with TELEGRAM_BOT_TOKEN}"
  printf '%s' "$TELEGRAM_BOT_TOKEN" >"$dir/telegram_token"
fi

{
  cat <<YAML
global:
  resolve_timeout: 5m

route:
  receiver: warn
  group_by: [alertname, service]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - matchers: [alertname="Watchdog"]
      receiver: watchdog
      group_wait: 0s
      group_interval: 1m
      repeat_interval: 1m
    # Railway backs up Postgres itself (no backup job reports here): these rules would fire for ever.
    - matchers: ['alertname=~"Backup.*|RestoreDrillFailed|RestoreNotVerified|WalArchiveStale|WalSpoolBacklog|BaseBackupStale|ObjectBackupStale"']
      receiver: blackhole
    - matchers: [severity="page"]
      receiver: page
      group_wait: 10s
      repeat_interval: 1h
    - matchers: [severity="warn"]
      receiver: warn
      repeat_interval: 12h

inhibit_rules:
  - source_matchers: [severity="page"]
    target_matchers: [severity="warn"]
    equal: [service]

receivers:
  - name: blackhole
YAML
  receiver page
  receiver warn
  echo "  - name: watchdog"
  if [ -n "${WATCHDOG_URL:-}" ]; then
    printf '%s' "$WATCHDOG_URL" >"$dir/watchdog_url"
    printf '    webhook_configs:\n      - url_file: %s/watchdog_url\n        send_resolved: false\n' "$dir"
  fi
} >"$dir/alertmanager.yml"

exec /bin/alertmanager --config.file="$dir/alertmanager.yml" --storage.path=/alertmanager --web.listen-address=:9093 --cluster.listen-address=
