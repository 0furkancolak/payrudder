import type { components } from "@/shared/api/schema";
import type { Tone } from "@/shared/ui/status-pill";

export type Dispute = components["schemas"]["Dispute"];
export const OUTCOMES = ["open", "won", "lost"] as const;

/** An open case is waiting on the seller or the bank (warning), a won one is over in the seller's favour, a lost one is money gone. */
export function disputeTone(outcome: string | undefined): Tone {
  return outcome === "open" ? "warning" : outcome === "won" ? "success" : outcome === "lost" ? "danger" : "neutral";
}

/** A chargeback takes the money back until it is decided; an inquiry (a warning ahead of one) does not. */
export const disputeKind = (d: Pick<Dispute, "formal">): "chargeback" | "inquiry" => (d.formal ? "chargeback" : "inquiry");

/** The one line that says what the case means now: the key under `disputes.means.*`. */
export function meaningKey(d: Pick<Dispute, "formal" | "outcome">): string {
  return d.outcome === "open" ? (d.formal ? "openChargeback" : "openInquiry") : d.outcome === "lost" ? "lost" : d.formal ? "wonChargeback" : "wonInquiry";
}

/** The evidence deadline matters while the case is open; once it is closed the date is history and is left out. */
export const dueShown = (d: Pick<Dispute, "outcome" | "evidence_due_time">): string | undefined => (d.outcome === "open" ? d.evidence_due_time : undefined);

/** The seller's own mark that a case was dealt with. It changes nothing at the provider; a case that is not open needs none. */
export const needsHandling = (d: Pick<Dispute, "outcome" | "handled">): boolean => d.outcome === "open" && !d.handled;

/** How close the evidence date is: past (`overdue`), within three days (`soon`), or later; none while the case is closed, marked as handled, or has no date. */
export function dueState(d: Pick<Dispute, "outcome" | "evidence_due_time" | "handled">, now: Date): "overdue" | "soon" | "later" | undefined {
  const due = needsHandling(d) ? d.evidence_due_time : undefined;
  if (!due) return undefined;
  const left = new Date(due).getTime() - now.getTime();
  if (Number.isNaN(left)) return undefined;
  return left < 0 ? "overdue" : left <= 3 * 24 * 3600 * 1000 ? "soon" : "later";
}

export type DisputeEvent = components["schemas"]["DisputeEvent"];
