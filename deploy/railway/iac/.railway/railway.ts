// Payrudder's Railway project as code: one project, the environments staging and production (plan 23, docs/runbooks/staging-and-production.md).
// This file owns the resources: services, databases, volumes, the gateway's domain and the canvas groups. It does not own variables:
// deploy/railway/bootstrap.sh sets those per environment. Each service's build and deploy settings come from its
// deploy/railway/{services,infra}/<name>.railway.json, so they are written once.
// Plan / apply with the environment linked: cd deploy/railway/iac && npm ci && railway config plan --environment staging (the bootstrap applies it).
import { readFileSync } from "node:fs";
import { defineRailway, empty, group, image, postgres, redis, service, volume } from "railway/iac";

const REGION = "europe-west4-drams3a";
const railwayDir = new URL("../../", import.meta.url);

// The build and deploy settings of a service's railway.json; requiredMountPath is left to the volume mount.
function settings(dir: "services" | "infra", name: string) {
  const cfg = JSON.parse(readFileSync(new URL(`${dir}/${name}.railway.json`, railwayDir), "utf8"));
  const { requiredMountPath: _, ...deploy } = cfg.deploy;
  // No watch patterns: nothing is built from GitHub, every deploy is an upload, and Railway skips an upload whose files match none of them.
  return cfg.build ? { build: { ...cfg.build, watchPatterns: [] }, deploy } : { deploy };
}

const vol = (name: string) =>
  volume(`${name}-volume`, { region: REGION, sizeMB: 5000, allowOnlineResize: true, alerts: { usage: { "80": {}, "95": {}, "100": {} } } });

export default defineRailway(({ isEnvironment }, project) => {
  const production = isEnvironment("production");
  // Not connected to GitHub: deploy-staging.yml uploads every push to main, deploy-production.yml a published release.
  const source = empty();
  // The gateway's custom domain is created by the bootstrap (`railway domain`; IaC cannot register one) and then kept here. The bootstrap
  // passes it once it exists, so an apply never deletes it and the first one does not fail.
  const apiDomain = process.env.PAYRUDDER_API_DOMAIN;
  const grafanaDomain = process.env.PAYRUDDER_GRAFANA_DOMAIN;
  const svc = (dir: "services" | "infra", name: string, extra: Record<string, unknown> = {}) =>
    service(name, { source, ...settings(dir, name), replicas: { [REGION]: 1 }, ...extra });

  const Postgres = postgres("Postgres", { region: REGION });
  Postgres.networking = { privateNetworkEndpoint: "postgres" };
  const Redis = redis("Redis", { region: REGION });
  Redis.networking = { privateNetworkEndpoint: "redis" };
  const volumes = {
    rabbitmq: vol("rabbitmq"),
    redpanda: vol("redpanda"),
    clickhouse: vol("clickhouse"),
    prometheus: vol("prometheus"),
    loki: vol("loki"),
    tempo: vol("tempo"),
  };

  // Railway's PgBouncer template (image and variable names), with one change to its start command (pgbouncer.railway.json): every database
  // ("*") and every role, their passwords read from Postgres (auth_query), not the template's single database and user. No public TCP proxy.
  const pgbouncer = svc("infra", "pgbouncer", { source: image("ghcr.io/cloudnative-pg/pgbouncer:1.25.2") });
  const rabbitmq = svc("infra", "rabbitmq", { volumeMounts: { "/var/lib/rabbitmq": volumes.rabbitmq } });
  const redpanda = svc("infra", "redpanda", { volumeMounts: { "/var/lib/redpanda/data": volumes.redpanda } });
  // ClickHouse and analytics-svc only in production (the bootstrap's profile full; staging is small).
  const clickhouse = production ? [svc("infra", "clickhouse", { volumeMounts: { "/var/lib/clickhouse": volumes.clickhouse } })] : [];

  const gateway = svc("services", "gateway", apiDomain ? { domains: [{ domain: apiDomain, port: 8000 }] } : {});
  const core = ["plans-svc", "ingest-svc", "aggregator", "limits-svc", "billing-svc", "assets-svc", ...(production ? ["analytics-svc"] : [])].map((n) =>
    svc("services", n),
  );
  const workers = ["payments-worker", "invoice-worker", "notifier"].map((n) => svc("services", n));
  const mockpsp = svc("services", "mockpsp");

  // Observability (docs/08-observability.md): metrics in Prometheus, traces in Tempo through the collector, logs in Loki through Locomotive
  // (Railway's log forwarder template) and Vector, alerts from Alertmanager to Telegram, Grafana over all of them.
  const observability = [
    svc("infra", "otel-collector"),
    svc("infra", "tempo", { volumeMounts: { "/var/tempo": volumes.tempo } }),
    svc("infra", "loki", { volumeMounts: { "/loki": volumes.loki } }),
    svc("infra", "vector"),
    svc("infra", "locomotive", { source: image("ghcr.io/brody192/locomotive@sha256:f9550f13f76e64109c75926c223cf2c0fa3c810cc489c59d8a133b83816a957f") }),
    svc("infra", "prometheus", { volumeMounts: { "/prometheus": volumes.prometheus } }),
    svc("infra", "alertmanager"),
    svc("infra", "grafana", grafanaDomain ? { domains: [{ domain: grafanaDomain, port: 3000 }] } : {}),
    svc("infra", "postgres-exporter", { source: image("quay.io/prometheuscommunity/postgres-exporter:v0.17.1") }),
    svc("infra", "redis-exporter", { source: image("oliver006/redis_exporter:v1.67.0-alpine") }),
  ];

  return project("payrudder", {
    variables: { managed: false },
    resources: [
      ...group("Data", [Postgres, pgbouncer, Redis, ...clickhouse]),
      ...group("Messaging", [rabbitmq, redpanda]),
      ...group("API", [gateway, ...core]),
      ...group("Workers", workers),
      ...group("Sandbox", [mockpsp]),
      ...group("Observability", observability),
      volumes.rabbitmq,
      volumes.redpanda,
      volumes.prometheus,
      volumes.loki,
      volumes.tempo,
      ...(production ? [volumes.clickhouse] : []),
    ],
  });
});
