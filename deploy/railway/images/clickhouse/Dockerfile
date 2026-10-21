# ClickHouse for Railway (analytics-svc only). Build context: the repo root. Mount a volume at /var/lib/clickhouse and set RAILWAY_RUN_UID=0.
# Service variables: CLICKHOUSE_DB=quotify, CLICKHOUSE_USER=quotify, CLICKHOUSE_PASSWORD, CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1, PORT=8123 (health: /ping).
FROM clickhouse/clickhouse-server:25.8.33.6-alpine
COPY deploy/clickhouse/low-memory.xml /etc/clickhouse-server/config.d/low-memory.xml
COPY deploy/railway/images/clickhouse/listen.xml /etc/clickhouse-server/config.d/listen.xml
