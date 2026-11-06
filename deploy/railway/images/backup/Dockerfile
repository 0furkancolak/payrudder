# Production backups on Railway: deploy/backup's pg_dump, restore drill and invariants into the private R2 bucket
# payrudder-backups-production (run.sh). pg_dump must not be older than the server (Railway's postgres-ssl:18). Build context: the repo root.
FROM postgres:18-alpine
RUN apk add --no-cache curl aws-cli
COPY deploy/backup /scripts
COPY deploy/railway/images/backup/run.sh /railway-run.sh
RUN chmod +x /scripts/*.sh
ENTRYPOINT ["/bin/sh", "/railway-run.sh"]
