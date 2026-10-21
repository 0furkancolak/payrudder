# One-shot job: databases and roles on Railway's managed Postgres. Build context: the repo root. Runs to completion; restart policy NEVER.
FROM postgres:17.11-alpine
COPY deploy/railway/images/postgres-init/init.sh /init.sh
ENTRYPOINT ["/bin/sh", "/init.sh"]
