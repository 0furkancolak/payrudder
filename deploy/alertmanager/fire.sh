#!/bin/sh
# Fires one page and one warn alert, and the Watchdog heartbeat is already firing by itself, at a running Alertmanager, to see the receiver path work end to end
# (make alert-test; locally the mail lands in Mailpit, http://localhost:42825). They resolve on their own after Alertmanager's resolve_timeout (5 minutes).
# Environment: ALERTMANAGER_URL (default http://localhost:42993).
set -eu
url="${ALERTMANAGER_URL:-http://localhost:42993}"
curl -fsS -X POST -H 'Content-Type: application/json' "$url/api/v2/alerts" -d '[
  {"labels": {"alertname": "TestPage", "severity": "page", "service": "alert-test"}, "annotations": {"summary": "a test page: if you read this, the page receiver works"}},
  {"labels": {"alertname": "TestWarn", "severity": "warn", "service": "alert-test-2"}, "annotations": {"summary": "a test warning: if you read this, the warn receiver works"}}
]'
echo "alert-test: fired TestPage and TestWarn at $url"
