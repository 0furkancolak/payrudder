import { tenantOf, type Environment } from "@/shared/lib/environment";
import { ALL_APPS } from "@/shared/lib/scope";
import { ALL_TENANTS } from "./nav";

/** Where a link goes: a path and its search. */
export interface Target {
  to: string;
  search: Record<string, string>;
}

interface AppLike {
  id?: string;
  tenants?: { id?: string; environment?: string; status?: string }[];
}

/** The environment an app can be shown in: the wanted one when the app has it, else the one it has (live first). */
export function envFor(app: AppLike | undefined, want: Environment): Environment {
  if (!app || tenantOf(app, want)) return want;
  return tenantOf(app, "live") ? "live" : "sandbox";
}

/** The first segment under /app/t/<id>: "/customers" for "/app/t/T/customers/42" ("" for the tenant's own path). */
function tenantSection(path: string): string {
  const m = /^\/app\/t\/[^/]+(\/[^/]+)?/.exec(path);
  return m?.[1] ?? "";
}

/** The part of an app page's path after the app's id: "/keys" for "/app/apps/A/keys". */
function appSection(path: string): string {
  const m = /^\/app\/apps\/[^/]+(\/.*)?$/.exec(path);
  return m?.[1] ?? "";
}

/**
 * Where choosing another scope (an app, or all apps) leads from this page: the same page of the other app when it exists there, so the person stays
 * where they were. An app's own pages (keys, settings...) have no "all apps" version, so choosing "all apps" there leads to the apps' dashboard.
 */
export function scopeTarget(path: string, to: string, apps: readonly AppLike[], env: Environment): Target {
  const app = apps.find((a) => a.id === to);
  const e = envFor(app, env);
  if (path.startsWith("/app/t/")) {
    const section = tenantSection(path);
    if (to === ALL_APPS) return { to: `/app/t/${ALL_TENANTS}${section}`, search: { app: ALL_APPS, env } };
    const tenant = tenantOf(app, e);
    if (tenant) return { to: `/app/t/${tenant}${section}`, search: { app: to } };
    return { to: `/app/apps/${to}`, search: { app: to, env: e } };
  }
  if (path.startsWith("/app/apps/")) {
    if (to === ALL_APPS) return { to: "/app", search: { app: ALL_APPS, env } };
    return { to: `/app/apps/${to}${appSection(path)}`, search: { app: to, env: e } };
  }
  if (path === "/app") {
    return to === ALL_APPS ? { to: "/app", search: { app: ALL_APPS, env } } : { to: `/app/apps/${to}`, search: { app: to, env: e } };
  }
  // The organisation's own pages (members, settings...) are the same for every app: only the scope in the address changes.
  return { to: path, search: { app: to, env: to === ALL_APPS ? env : e } };
}

/** Where choosing the other environment leads from this page: the same page of the same app in the other environment. Undefined when there is none. */
export function envTarget(path: string, scope: string, app: AppLike | undefined, env: Environment): Target | undefined {
  if (path.startsWith("/app/t/")) {
    const section = tenantSection(path);
    if (scope === ALL_APPS) return { to: `/app/t/${ALL_TENANTS}${section}`, search: { app: ALL_APPS, env } };
    const tenant = tenantOf(app, env);
    return tenant ? { to: `/app/t/${tenant}${section}`, search: { app: scope } } : undefined;
  }
  return { to: path, search: { app: scope, env } };
}
