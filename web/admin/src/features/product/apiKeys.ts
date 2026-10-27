import type { components } from "@/shared/api/schema";

/**
 * What an API key may do (phase 53). The vocabulary is the gateway's (pkg/apikey): the type comes from the API's own schema, so a name the gateway does not
 * know does not compile, and a test over the gateway's OpenAPI checks that this list and the code enforce the same set.
 */
export type KeyPermission = components["schemas"]["ApiKeyPermission"];
export type KeyRole = components["schemas"]["ApiKeyRole"];

/** The roles, in the order they are offered. A fixed role is a named set; custom is any set. */
export const KEY_ROLES: readonly KeyRole[] = ["admin", "billing", "read_only", "ingest_only", "custom"];

/** The role a new key starts on: read-only, so a key made in a hurry cannot change anything; a wider role is a deliberate choice. */
export const DEFAULT_KEY_ROLE: KeyRole = "read_only";

/** One resource of the API and the permissions it has: most have a read and a write, a few only one. */
export interface KeyResource {
  resource: string;
  read?: KeyPermission;
  write?: KeyPermission;
}

export const KEY_RESOURCES: readonly KeyResource[] = [
  { resource: "events", write: "events:write" },
  { resource: "customers", read: "customers:read", write: "customers:write" },
  { resource: "subscriptions", read: "subscriptions:read", write: "subscriptions:write" },
  { resource: "catalog", read: "catalog:read", write: "catalog:write" },
  { resource: "usage", read: "usage:read" },
  { resource: "credits", read: "credits:read", write: "credits:write" },
  { resource: "invoices", read: "invoices:read", write: "invoices:write" },
  { resource: "reports", read: "reports:read" },
  { resource: "accounting", read: "accounting:read", write: "accounting:write" },
  { resource: "webhooks", read: "webhooks:read", write: "webhooks:write" },
  { resource: "affiliates", read: "affiliates:read", write: "affiliates:write" },
  { resource: "settings", read: "settings:read", write: "settings:write" },
  { resource: "assets", read: "assets:read", write: "assets:write" },
  { resource: "sandbox", write: "sandbox:write" },
];

export const ALL_PERMISSIONS: readonly KeyPermission[] = KEY_RESOURCES.flatMap((r) => [r.read, r.write].filter((p): p is KeyPermission => p !== undefined));

/** The set of a fixed role (the same as the gateway's pkg/apikey RolePermissions); null for custom, which has no fixed set. */
export function permissionsOfRole(role: KeyRole): readonly KeyPermission[] | null {
  switch (role) {
    case "admin":
      return ALL_PERMISSIONS;
    case "read_only":
      return ALL_PERMISSIONS.filter((p) => p.endsWith(":read"));
    case "ingest_only":
      return ["events:write"];
    case "billing":
      return ["events:write", "customers:read", "customers:write", "subscriptions:read", "subscriptions:write", "catalog:read", "usage:read", "credits:read", "credits:write", "invoices:read", "invoices:write"];
    case "custom":
      return null;
  }
}

/** The request body of a new key: a fixed role names itself; a custom key lists what it was given. Nothing chosen for a custom key is not a key. */
export function keyRequest(role: KeyRole, chosen: ReadonlySet<KeyPermission>): { role: KeyRole; permissions?: KeyPermission[] } | null {
  if (role !== "custom") return { role };
  const permissions = ALL_PERMISSIONS.filter((p) => chosen.has(p));
  return permissions.length === 0 ? null : { role, permissions };
}

/** The role of a key as the API sends it; a key from before roles (only a scope) reads as the role the scope stood for. */
export function roleOfKey(k: { role?: string; scope?: string }): KeyRole {
  if (k.role && (KEY_ROLES as readonly string[]).includes(k.role)) return k.role as KeyRole;
  if (k.scope === "SCOPE_INGEST") return "ingest_only";
  if (k.scope === "SCOPE_READ") return "read_only";
  return "admin";
}

/** How many permissions a key holds, for the list ("12 of 24"). The role admin always holds everything. */
export function permissionCount(k: { role?: string; permissions?: readonly string[] }): number {
  return k.role === "admin" ? ALL_PERMISSIONS.length : (k.permissions ?? []).length;
}
