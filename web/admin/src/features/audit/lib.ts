import type { components } from "@/shared/api/schema";

// The audit log (plan 22, G4): the pure parts. The calls are in api.ts, the page in AuditLog.vue.

export type AuditEvent = components["schemas"]["AuditEvent"];
export type ActorType = AuditEvent["actor_type"];

/** The areas of the log (the part of an action before its dot), in the order the filter lists them. */
export const AUDIT_AREAS = ["api_key", "member", "plan", "webhook_endpoint", "customer", "credit_note", "invoice", "provider_account", "tenant", "price_change", "coupon", "promotion_code", "routing_rules", "tax_rates", "mail_settings", "mail_integration", "go_live", "contract", "support", "dunning", "invoice_payment", "bank_transfer", "subscription", "reconciliation", "audit", "user", "two_factor", "sign_in", "account"] as const;

export const ACTOR_TYPES: readonly ActorType[] = ["user", "api_key", "operator", "system"];

/**
 * The actions that have a translated label (`audit.actions.<area>.<what>`). An action the console does not know is shown as the server named it, so a
 * new one still appears; it is not a failure.
 */
export const KNOWN_ACTIONS: readonly string[] = [
  "api_key.created", "api_key.rotated", "api_key.revoked", "api_key.expiring",
  "member.invited", "member.joined", "member.role_changed", "member.removed",
  "plan.created", "plan.version_published",
  "webhook_endpoint.created", "webhook_endpoint.updated", "webhook_endpoint.deleted", "webhook_endpoint.secret_rotated",
  "customer.blocked", "customer.unblocked", "customer.contact_changed", "customer.address_changed", "customer.tax_changed", "customer.payment_method_changed",
  "subscription.coupon_applied", "subscription.coupon_removed",
  "credit_note.issued", "credit_note.refunded", "credit_note.refund_failed", "credit_note.refund_retried",
  "provider_account.connected", "provider_account.replaced", "provider_account.removed", "provider_account.default_changed",
  "tenant.status_requested", "tenant.status_changed", "tenant.settings_updated",
  "two_factor.enabled", "two_factor.disabled", "two_factor.recovery_codes_replaced", "two_factor.recovery_code_used", "two_factor.reset_by_operator",
  "price_change.scheduled", "price_change.withdrawn",
  "coupon.created", "coupon.deactivated", "promotion_code.created", "promotion_code.deactivated",
  "routing_rules.changed", "tax_rates.changed", "mail_settings.changed", "mail_integration.saved", "mail_integration.removed", "mail_integration.default_set", "mail_integration.routing_changed",
  "go_live.billing_profile_saved", "go_live.terms_accepted", "go_live.activated",
  "contract.version_published", "contract.settings_changed", "support.status_changed", "dunning.settings_changed",
  "invoice_payment.recorded", "bank_transfer.receipt_recorded", "user.password_reset", "user.sign_in_locked",
  "reconciliation.discrepancy_explained", "reconciliation.discrepancy_resolved", "reconciliation.discrepancy_assigned", "audit.pruned",
];

/** The i18n key of an action's label, or null when the console has none for it (show the action itself). */
export function actionLabelKey(action: string): string | null {
  return KNOWN_ACTIONS.includes(action) ? `audit.actions.${action}` : null;
}

/** What the filter holds. The dates are YYYY-MM-DD in the browser's calendar, both empty for any time. */
export interface AuditFilter {
  areas: string[];
  actor: string;
  range: { from: string; to: string };
  /** The operator's log only: one tenant's id. */
  tenant: string;
}

export const emptyFilter = (): AuditFilter => ({ areas: [], actor: "", range: { from: "", to: "" }, tenant: "" });

export const isFiltered = (f: AuditFilter): boolean => f.areas.length > 0 || f.actor !== "" || f.range.from !== "" || f.range.to !== "" || f.tenant !== "";

/** The start of a day (plus `plus` days) in the browser's calendar as RFC 3339 in UTC; empty for no date. */
export function dayStart(day: string, plus = 0): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return "";
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + plus).toISOString();
}

export interface AuditQuery {
  /** Names or areas (`api_key.*`), separated by commas. */
  action?: string;
  actor?: string;
  from?: string;
  to?: string;
  tenant_id?: string;
}

/**
 * The query of a filter. An area is sent as `<area>.*`; the log keeps an entry for `from` on (inclusive) and before `to` (exclusive), so the last day of the
 * range is sent as the start of the day after it. Empty parts are left out.
 */
export function auditQuery(f: AuditFilter): AuditQuery {
  const from = dayStart(f.range.from);
  const to = dayStart(f.range.to, 1);
  const actor = f.actor.trim();
  const tenant = f.tenant.trim();
  return {
    ...(f.areas.length > 0 ? { action: f.areas.map((a) => `${a}.*`).join(",") } : {}),
    ...(actor !== "" ? { actor } : {}),
    ...(from !== "" ? { from } : {}),
    ...(to !== "" ? { to } : {}),
    ...(tenant !== "" ? { tenant_id: tenant } : {}),
  };
}

export const AUDIT_FILE_NAME = "audit-log.csv";

/** The detail of an entry, read for showing: a JSON object (or anything JSON) pretty-printed; text that is not JSON as it came; "" for nothing. */
export function prettyDetail(json: string): string {
  const raw = json.trim();
  if (raw === "" || raw === "{}" || raw === "null") return "";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return json;
  }
}

/** Who did it, as one line: the e-mail address if the entry has one, else the id. */
export function actorLine(e: Pick<AuditEvent, "actor_email" | "actor_id">): string {
  return e.actor_email || e.actor_id;
}

/** What the entry is about, as one line: the kind and the id. */
export function targetLine(e: Pick<AuditEvent, "target_type" | "target_id">): string {
  return [e.target_type, e.target_id].filter((x) => x !== "").join(" · ");
}
