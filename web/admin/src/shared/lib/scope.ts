import { safeGet, safeSet } from "./storage";

/**
 * The scope of the console: which app the lists, dashboards and reports are about. An app is always selected; "all" is the one explicit choice that
 * spans every app of the organisation. The scope lives in the address (`?app=<id|all>`), so a link shares it and a reload keeps it.
 */
export const ALL_APPS = "all";

const LAST_APP_KEY = "payrudder.app";

/** The app (or "all") the address names, or undefined when it names nothing usable. */
export function appInSearch(search: Record<string, unknown> | undefined): string | undefined {
  const v = search?.app;
  return typeof v === "string" && v !== "" && v.length <= 64 ? v : undefined;
}

/** Remembers the app the person last worked in (per browser; a blocked storage just forgets). "all" is a choice for the moment, not remembered. */
export function rememberApp(id: string): void {
  if (id && id !== ALL_APPS) safeSet("local", LAST_APP_KEY, id);
}

export function lastApp(): string | null {
  return safeGet("local", LAST_APP_KEY);
}

interface HasId {
  id?: string;
}

/**
 * Works out the scope: the app the path is about wins (an app or tenant page is that app's), then the address, then the app used last, then the first
 * app. `null` only while there are no apps (or they have not arrived), so nothing is chosen before there is something to choose from.
 */
export function resolveScope(input: { pathApp?: string; addressApp?: string; apps: readonly HasId[] | undefined; last?: string | null }): string | null {
  const apps = input.apps ?? [];
  const has = (id: string | undefined | null): id is string => !!id && apps.some((a) => a.id === id);
  if (has(input.pathApp)) return input.pathApp;
  if (input.addressApp === ALL_APPS && apps.length > 0) return ALL_APPS;
  if (has(input.addressApp)) return input.addressApp;
  if (has(input.last)) return input.last;
  return apps[0]?.id ?? null;
}
