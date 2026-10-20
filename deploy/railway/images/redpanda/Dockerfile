# Redpanda for Railway (no Railway-native Kafka). Build context: the repo root. Mount a volume at /var/lib/redpanda/data and set RAILWAY_RUN_UID=0
# (Railway volumes are root-owned). Health: the admin API, so the service variable PORT must be 9644.
FROM redpandadata/redpanda:v26.2.5
COPY deploy/railway/images/redpanda/start.sh /railway-start.sh
ENTRYPOINT ["/bin/sh", "/railway-start.sh"]
