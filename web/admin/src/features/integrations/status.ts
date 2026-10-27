import type { ConnectionState } from "@/features/providers/connections";

/**
 * What a card says about an integration: the states of a payment provider (not connected, working, a problem) plus two for the ones that need no connection:
 * "always" (on whatever happens: a simulator, the built-in sender) and "unused" (there, but something else is chosen instead).
 */
export type CardState = ConnectionState | "always" | "unused";

export const stateTone = (s: CardState): "success" | "danger" | "neutral" | "info" => (s === "working" ? "success" : s === "problem" ? "danger" : s === "always" ? "info" : "neutral");

interface EInvoiceAccount {
  provider?: string;
  mode?: string;
}

/** The integrator of the live environment: connected when it is this one (an environment has one account, so connecting the other replaces it). */
export function eInvoiceState(account: EInvoiceAccount | null | undefined, provider: "nilvera" | "webhook"): { state: CardState; mode: string } {
  if (!account || account.provider !== provider) return { state: "none", mode: "" };
  return { state: "working", mode: account.mode ?? "" };
}

interface MailConnection {
  connected?: boolean;
  integration?: { check_problem?: string; checked_time?: string | null } | null;
}

/** The tenant's own e-mail provider: not connected, working, or one the provider refused or could not be reached with at the last check. */
export function ownMailState(c: MailConnection | null | undefined): CardState {
  if (!c?.connected) return "none";
  return c.integration?.check_problem ? "problem" : "working";
}

/** Payrudder's own sender: on, unless the tenant connected its own provider, which then carries the live mail. */
export function builtInMailState(c: MailConnection | null | undefined): CardState {
  return c?.connected ? "unused" : "always";
}
