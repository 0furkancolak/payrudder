# PgBouncer for Railway, in front of the managed Postgres (transaction pooling, the settings of deploy/compose/pgbouncer/pgbouncer.ini). Only the APP role of a
# service goes through it; the system role, the migrator and PAYRUDDER_PG_ADMIN_DSN reach postgres.railway.internal directly (docs/runbooks/scaling.md).
# Variables: PGBOUNCER_AUTH_PASSWORD (the postgres user's, ${{Postgres.PGPASSWORD}}), PG_HOST (default postgres.railway.internal), PORT 6432.
FROM edoburu/pgbouncer@sha256:7d7a27d9e90985cab5cf42256f5c13a3120baa4b055b69df37beb272b89b2340
COPY deploy/railway/images/pgbouncer/start.sh /railway-start.sh
ENTRYPOINT ["/bin/sh", "/railway-start.sh"]
