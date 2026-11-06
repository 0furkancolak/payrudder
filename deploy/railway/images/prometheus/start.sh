#!/bin/sh
# Fills in the environment's name, and drops the targets marked "# production" outside production: staging has no ClickHouse or analytics-svc,
# and ServiceDown would fire there for ever.
set -eu
env=${RAILWAY_ENVIRONMENT_NAME:-unknown}
drop='/# production$/d'
[ "$env" = production ] && drop='/^$/!b'
sed -e "s/\${RAILWAY_ENVIRONMENT_NAME}/$env/" -e "$drop" /etc/prometheus/prometheus.railway.yml >/tmp/prometheus.yml
exec /bin/prometheus --config.file=/tmp/prometheus.yml --storage.tsdb.path=/prometheus --storage.tsdb.retention.time=15d \
  --storage.tsdb.retention.size=4GB --web.enable-remote-write-receiver --web.listen-address=:9090
