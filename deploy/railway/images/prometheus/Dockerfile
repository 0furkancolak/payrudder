# Prometheus for Railway, with the repo's alert rules. Build context: the repo root. Mount a volume at /prometheus and set RAILWAY_RUN_UID=0.
FROM prom/prometheus:v3.15.0
COPY deploy/railway/images/prometheus/prometheus.yml /etc/prometheus/prometheus.railway.yml
COPY deploy/prometheus/rules/payrudder.yaml /etc/prometheus/rules/payrudder.yaml
COPY deploy/railway/images/prometheus/start.sh /railway-start.sh
ENTRYPOINT ["/bin/sh", "/railway-start.sh"]
