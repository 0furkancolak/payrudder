import { closeName, openName } from "@/shared/lib/sheets";

/**
 * A record opened over the sheet that mentions it, not by leaving the page: its name in the stack of sheets is `peek:<kind>:<tenant>:<id>`, so the
 * address holds it like any sheet and a reload brings it back. Only the kinds with a sheet that stands on its own (PeekHost) can be peeked.
 */
export const PEEK_KINDS = ["invoice", "customer", "subscription"] as const;
export type PeekKind = (typeof PEEK_KINDS)[number];

export const peekName = (kind: PeekKind, tenantId: string, id: string) => `peek:${kind}:${tenantId}:${encodeURIComponent(id)}`;
export function parsePeek(name: string): { kind: PeekKind; tenantId: string; id: string } | null {
  const [head, kind, tenantId, id] = name.split(":");
  if (head !== "peek" || !(PEEK_KINDS as readonly string[]).includes(kind ?? "") || !tenantId || !id) return null;
  return { kind: kind as PeekKind, tenantId, id: decodeURIComponent(id) };
}
export const peek = (kind: PeekKind, tenantId: string, id: string) => openName(peekName(kind, tenantId, id));
export const unpeek = (kind: PeekKind, tenantId: string, id: string) => closeName(peekName(kind, tenantId, id));
