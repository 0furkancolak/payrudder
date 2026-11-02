import { ALL_APPS } from "@/shared/lib/scope";

/** The three states in which the operator has taken a live environment out of service (plan 14). */
export type Hold = "suspended" | "frozen" | "banned";

const HOLDS = new Map<string, Hold>([["TENANT_STATUS_SUSPENDED", "suspended"], ["TENANT_STATUS_FROZEN", "frozen"], ["TENANT_STATUS_BANNED", "banned"]]);

/** The hold a tenant status is, or undefined for any other status (active, not live yet, unknown). */
export const holdOf = (status: string | undefined): Hold | undefined => (status ? HOLDS.get(status) : undefined);

interface HasTenants {
  id?: string;
  name?: string;
  tenants?: { environment?: string; status?: string }[];
}

/**
 * The apps whose live environment is on hold, in the scope of the page: the one app, or every app of the organisation for the scope "all" (or none yet). Only a live
 * environment has a status (a sandbox is always active). The banner names each of them; the sandbox of such an app is no excuse to say nothing, because the person
 * may well be looking at it.
 */
export function heldApps(apps: readonly HasTenants[] | undefined, scope: string | null | undefined): { id: string; name: string; hold: Hold }[] {
  const out: { id: string; name: string; hold: Hold }[] = [];
  for (const a of apps ?? []) {
    if (scope && scope !== ALL_APPS && a.id !== scope) continue;
    const hold = holdOf(a.tenants?.find((t) => t.environment === "ENVIRONMENT_LIVE")?.status);
    if (hold) out.push({ id: a.id ?? "", name: a.name ?? "", hold });
  }
  return out;
}
