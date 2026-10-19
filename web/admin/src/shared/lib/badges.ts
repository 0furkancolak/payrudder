import type { BadgeVariants } from "@/shared/ui/badge";

type Variant = NonNullable<BadgeVariants["variant"]>;

export function sagaVariant(state: string | undefined): Variant {
  if (state === "COMPLETED") return "success";
  if (state === "PAYMENT_DECLINED" || state === "FAILED" || state === "VOID") return "destructive";
  return "secondary";
}

export function statusVariant(status: string | undefined): Variant {
  if (status === "paid") return "success";
  if (status === "open") return "secondary";
  if (status === "void" || status === "uncollectible") return "destructive";
  return "secondary";
}
