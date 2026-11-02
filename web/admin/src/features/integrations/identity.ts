/** The three records of a sender's domain that the identity check reads, in the order the sheet lists them. */
export const IDENTITY_CHECKS = ["spf", "dkim", "dmarc"] as const;
export type IdentityCheck = (typeof IDENTITY_CHECKS)[number];

export const IDENTITY_STATUSES = ["ok", "weak", "missing", "invalid", "unchecked", "error"] as const;
export type IdentityStatus = (typeof IDENTITY_STATUSES)[number];

/** How a verdict is painted: a published record is good, a weak one is a warning, a missing or invalid one is a problem; "not checked" and "could not be checked" say nothing. */
export const identityTone = (s: IdentityStatus): "success" | "warning" | "danger" | "neutral" =>
  s === "ok" ? "success" : s === "weak" ? "warning" : s === "missing" || s === "invalid" ? "danger" : "neutral";
