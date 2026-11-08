# Alertmanager for Railway: the routing of deploy/alertmanager, delivered to Telegram. Build context: the repo root.
FROM prom/alertmanager:v0.28.1
COPY deploy/railway/images/alertmanager/start.sh /railway-start.sh
ENTRYPOINT ["/bin/sh", "/railway-start.sh"]
