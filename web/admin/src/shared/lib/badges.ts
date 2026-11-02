import type { BadgeVariants } from "@/shared/ui/badge";

type Variant = NonNullable<BadgeVariants["variant"]>;

export function sagaVariant(state: string | undefined): Variant {
  if (state === "COMPLETED") return "success";
  if (state === "PAYMENT_DECLINED" || state === "FAILED" || state === "VOID") return "destructive";
  // Waiting for money that is on its way (a bank transfer), for the customer to confirm it (3-D Secure) or for the gate to let the first charge through: neither done nor failed.
  if (state === "PAYMENT_PENDING" || state === "PAYMENT_ACTION_REQUIRED" || state === "PAYMENT_HELD") return "outline";
  return "secondary";
}

export function statusVariant(status: string | undefined): Variant {
  if (status === "paid") return "success";
  if (status === "void" || status === "uncollectible") return "destructive";
  return "secondary";
}
