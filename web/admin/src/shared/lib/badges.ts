import type { BadgeVariants } from "@/shared/ui/badge";

type Variant = NonNullable<BadgeVariants["variant"]>;

export function sagaVariant(state: string | undefined): Variant {
  if (state === "COMPLETED") return "success";
  if (state === "PAYMENT_DECLINED" || state === "FAILED" || state === "VOID") return "destructive";
  // Waiting for money that is on its way (a bank transfer): neither done nor failed.
  if (state === "PAYMENT_PENDING") return "outline";
  return "secondary";
}

export function statusVariant(status: string | undefined): Variant {
  if (status === "paid") return "success";
  if (status === "void" || status === "uncollectible") return "destructive";
  return "secondary";
}
