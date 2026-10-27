import { safeGet, safeSet } from "./storage";

/** The two environments of an app. Each has its own tenant, keys, data and providers. */
export type Environment = "live" | "sandbox";

/** What the API calls them. */
export type EnvironmentEnum = "ENVIRONMENT_LIVE" | "ENVIRONMENT_SANDBOX";

export function isEnvironment(v: unknown): v is Environment {
  return v === "live" || v === "sandbox";
}

/** The API's word for an environment ("sandbox" -> "ENVIRONMENT_SANDBOX"). */
export function envEnum(env: Environment): EnvironmentEnum {
  return env === "live" ? "ENVIRONMENT_LIVE" : "ENVIRONMENT_SANDBOX";
}

/** The environment an API value names: sandbox only for ENVIRONMENT_SANDBOX, so a value the console does not know reads as live (what a tenant is by default). */
export function envOf(value: string | undefined): Environment {
  return value === "ENVIRONMENT_SANDBOX" ? "sandbox" : "live";
}

/** How an API key is shown without its secret part: "qk_live_ab12" or "qk_test_ab12". */
export function keyMask(env: string, prefix: string | undefined): string {
  return `qk_${env === "live" ? "live" : "test"}_${prefix ?? ""}`;
}

interface HasTenants {
  tenants?: { id?: string; environment?: string }[];
}

/** The tenant of an app for an environment, or "" when the app has none. */
export function tenantOf(app: HasTenants | undefined, env: Environment): string {
  const want = envEnum(env);
  return app?.tenants?.find((t) => t.environment === want)?.id ?? "";
}

// --- which environment the console opens an app in (phase 45 F)

/** The status of an app's live tenant as the console tells it: "none" when the app has no live tenant (a sandbox-only deployment). */
export type LiveState = "none" | "inactive" | "active" | "suspended";

interface HasStatus {
  tenants?: { id?: string; environment?: string; status?: string }[];
}

/** The live tenant of an app, if it has one. */
export function liveTenant<T extends { environment?: string }>(app: { tenants?: T[] } | undefined): T | undefined {
  return app?.tenants?.find((t) => t.environment === "ENVIRONMENT_LIVE");
}

export function liveState(app: HasStatus | undefined): LiveState {
  const live = liveTenant(app);
  if (!live) return "none";
  switch (live.status) {
    case "TENANT_STATUS_ACTIVE":
      return "active";
    case "TENANT_STATUS_SUSPENDED":
      return "suspended";
    default:
      return "inactive";
  }
}

const LAST_ENV_KEY = "payrudder.env.";

/** Remembers the environment the person last worked in for an app (per browser; a blocked storage just forgets). */
export function rememberEnv(appId: string, env: Environment): void {
  if (appId) safeSet("local", LAST_ENV_KEY + appId, env);
}

export function lastEnv(appId: string): Environment | null {
  const v = safeGet("local", LAST_ENV_KEY + appId);
  return isEnvironment(v) ? v : null;
}

/**
 * The environment an app opens in when the address does not say: the one the person used last, if the app still has it; otherwise live when it is active,
 * else sandbox. The same input always gives the same answer, so a page never flips on its own.
 */
export function defaultEnv(app: (HasTenants & HasStatus & { id?: string }) | undefined): Environment {
  if (!app) return "live";
  const last = app.id ? lastEnv(app.id) : null;
  if (last && tenantOf(app, last) !== "") return last;
  if (liveState(app) === "active") return "live";
  return tenantOf(app, "sandbox") !== "" ? "sandbox" : "live";
}

/** The environment the organisation's home opens in: live when any app is active there, else sandbox when any app has one. */
export function defaultOrgEnv(apps: readonly (HasTenants & HasStatus)[] | undefined): Environment {
  if ((apps ?? []).some((a) => liveState(a) === "active")) return "live";
  return (apps ?? []).some((a) => tenantOf(a, "sandbox") !== "") ? "sandbox" : "live";
}

/** Finds a tenant by id among a person's apps, with the app it belongs to. */
export function findTenant<A extends HasTenants>(apps: readonly A[] | undefined, id: string): { app: A; tenant: NonNullable<A["tenants"]>[number]; env: Environment } | null {
  for (const app of apps ?? []) {
    const tenant = app.tenants?.find((x) => x.id === id);
    if (tenant) return { app, tenant: tenant as NonNullable<A["tenants"]>[number], env: envOf(tenant.environment) };
  }
  return null;
}
