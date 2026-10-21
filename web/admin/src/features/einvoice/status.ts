import type { BadgeVariants } from "@/shared/ui/badge";

type Variant = NonNullable<BadgeVariants["variant"]>;

/** The badge colour of a document status. */
export function docVariant(status: string | undefined): Variant {
  if (status === "issued") return "success";
  if (status === "failed") return "destructive";
  return "secondary";
}

/** What an operator may do with a document: retry a failed one, cancel an issued one. */
export function docActions(d: { status?: string }): { retry: boolean; cancel: boolean } {
  return { retry: d.status === "failed", cancel: d.status === "issued" };
}

/** The message path of an error key: "einvoice.account.missing" is looked up at "einvoice.errorKeys.einvoice.account.missing". */
export function errorKeyPath(key: string | undefined): string {
  return key ? `einvoice.errorKeys.${key}` : "";
}
