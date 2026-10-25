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

/** Finds a tenant by id among a person's apps, with the app it belongs to. */
export function findTenant<A extends HasTenants>(apps: readonly A[] | undefined, id: string): { app: A; tenant: NonNullable<A["tenants"]>[number]; env: Environment } | null {
  for (const app of apps ?? []) {
    const tenant = app.tenants?.find((x) => x.id === id);
    if (tenant) return { app, tenant: tenant as NonNullable<A["tenants"]>[number], env: envOf(tenant.environment) };
  }
  return null;
}
