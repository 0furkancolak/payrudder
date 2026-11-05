// Payrudder's Railway project as code: one project, the environments staging and production (plan 23, docs/runbooks/staging-and-production.md).
// This file owns the resources: services, databases, volumes, the gateway's domain and the canvas groups. It does not own variables:
// deploy/railway/bootstrap.sh sets those per environment. Each service's build and deploy settings come from its
// deploy/railway/{services,infra}/<name>.railway.json, so they are written once.
// Plan / apply with the environment linked: cd deploy/railway/iac && npm ci && railway config plan --environment staging (the bootstrap applies it).
import { readFileSync } from "node:fs";
import { defineRailway, empty, github, group, postgres, redis, service, volume } from "railway/iac";

const REGION = "europe-west4-drams3a";
const railwayDir = new URL("../../", import.meta.url);

// The build and deploy settings of a service's railway.json; requiredMountPath is left to the volume mount.
function settings(dir: "services" | "infra", name: string) {
  const cfg = JSON.parse(readFileSync(new URL(`${dir}/${name}.railway.json`, railwayDir), "utf8"));
  const { requiredMountPath: _, ...deploy } = cfg.deploy;
  return { build: cfg.build, deploy };
}

const vol = (name: string) =>
  volume(`${name}-volume`, { region: REGION, sizeMB: 5000, allowOnlineResize: true, alerts: { usage: { "80": {}, "95": {}, "100": {} } } });

export default defineRailway(({ isEnvironment }, project) => {
  const production = isEnvironment("production");
  // Staging builds every push to main from GitHub; production is not connected: a published release uploads it (deploy-production.yml).
  const source = production ? empty() : github("0furkancolak/payrudder");
  // The gateway's custom domain is created by the bootstrap (`railway domain`; IaC cannot register one) and then kept here. The bootstrap
  // passes it once it exists, so an apply never deletes it and the first one does not fail.
  const apiDomain = process.env.PAYRUDDER_API_DOMAIN;
  const svc = (dir: "services" | "infra", name: string, extra: Record<string, unknown> = {}) =>
    service(name, { source, ...settings(dir, name), replicas: { [REGION]: 1 }, ...extra });

  const Postgres = postgres("Postgres", { region: REGION });
  Postgres.networking = { privateNetworkEndpoint: "postgres" };
  const Redis = redis("Redis", { region: REGION });
  Redis.networking = { privateNetworkEndpoint: "redis" };
  const volumes = { rabbitmq: vol("rabbitmq"), redpanda: vol("redpanda"), clickhouse: vol("clickhouse") };

  const pgbouncer = svc("infra", "pgbouncer");
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

  return project("payrudder", {
    variables: { managed: false },
    resources: [
      ...group("Data", [Postgres, pgbouncer, Redis, ...clickhouse]),
      ...group("Messaging", [rabbitmq, redpanda]),
      ...group("API", [gateway, ...core]),
      ...group("Workers", workers),
      ...group("Sandbox", [mockpsp]),
      volumes.rabbitmq,
      volumes.redpanda,
      ...(production ? [volumes.clickhouse] : []),
    ],
  });
});
