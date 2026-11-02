import type { components } from "@/shared/api/schema";

export type PaymentWait = components["schemas"]["PaymentWait"];

/** The two saga states in which an invoice waits for somebody other than the provider: for the seller or the operator to clear a hold, and for the customer to confirm. */
export const HELD = "PAYMENT_HELD";
export const ACTION_REQUIRED = "PAYMENT_ACTION_REQUIRED";

/** Whether the invoice's payment waits for one of those, so the sheet is worth asking what for. */
export const waits = (sagaState: string | undefined): boolean => sagaState === HELD || sagaState === ACTION_REQUIRED;

/** Why a first charge is held, in the words the sheet has: `unknown` when the payment gate could not be asked. */
export type HoldReason = "customer_blocked" | "tenant_on_hold" | "none" | "unknown";
export const holdReason = (w: Pick<PaymentWait, "hold_reason">): HoldReason => w.hold_reason ?? "unknown";

/**
 * What the person looking may do about a held first charge. Release asks the payment gate again and charges when it lets the invoice through; Void ends the invoice. Neither
 * is offered to a viewer (the server refuses their writes), and neither while the operator has the environment on hold (the gateway refuses every write then, and releasing
 * could not charge anyway).
 */
export function heldActions(w: Pick<PaymentWait, "state" | "hold_reason">, canWrite: boolean): { release: boolean; void: boolean } {
  const can = canWrite && w.state === HELD && holdReason(w) !== "tenant_on_hold";
  return { release: can, void: can };
}

/** What Release did, from the saga state it answers: the invoice is charged (`released`) or the gate still said no (`stillHeld`). */
export const releaseOutcome = (state: string | undefined): "released" | "stillHeld" => (state === HELD ? "stillHeld" : "released");
