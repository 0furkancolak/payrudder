import type { components } from "@/shared/api/schema";
import { toApiError } from "@/shared/api/client";

type S = components["schemas"];
export type OrganizationPlan = S["OrganizationPlan"];
export type PlanOffer = S["PlanOffer"];
export type PlanQuota = S["PlanQuota"];
export type PlanLimit = S["PlanLimit"];
export type PlanInvoice = S["PlanInvoice"];
export type OrganizationRow = S["OrganizationRow"];
export type PlatformInfo = S["PlatformInfo"];

/** The resources a plan limits, in the order the page shows them. */
export const RESOURCES = ["events", "apps", "members", "api_keys"] as const;
export type Resource = (typeof RESOURCES)[number];

/** A limit of 0 means unlimited, as everywhere in Payrudder. */
export const isUnlimited = (limit: number | undefined): boolean => !limit || limit <= 0;

/** How full a quota is, 0..100 (an unlimited quota is never full). */
export function quotaPercent(q: Partial<Pick<PlanQuota, "used" | "limit" | "unlimited">>): number {
  if (q.unlimited || isUnlimited(q.limit)) return 0;
  return Math.max(0, Math.min(100, Math.round(((q.used ?? 0) / (q.limit ?? 1)) * 100)));
}

export type Tone = "ok" | "warning" | "danger";

/** The colour of a quota bar: red once over the limit (whatever the mode), amber from 80 percent. */
export function quotaTone(q: Partial<Pick<PlanQuota, "used" | "limit" | "unlimited" | "over_limit">>): Tone {
  if (q.over_limit || quotaPercent(q) >= 100) return "danger";
  return quotaPercent(q) >= 80 ? "warning" : "ok";
}

export type Move = "current" | "upgrade" | "downgrade";

/** What choosing an offer would be, judged by the monthly price (a plan that costs more is an upgrade). */
export function offerMove(offer: Pick<PlanOffer, "current" | "base_fee_minor">, current: Pick<PlanOffer, "base_fee_minor"> | undefined): Move {
  if (offer.current) return "current";
  return (offer.base_fee_minor ?? 0) >= (current?.base_fee_minor ?? 0) ? "upgrade" : "downgrade";
}

/** The limit of a resource in an offer (undefined when the offer does not name it: unlimited). */
export const limitOf = (offer: Pick<PlanOffer, "limits">, resource: string): PlanLimit | undefined => offer.limits?.find((l) => l.resource === resource);

/** Whether an API error means "your plan's limit is reached", so the console offers the upgrade. */
export function isUpgradeError(error: unknown): boolean {
  if (!error) return false;
  const key = toApiError(error).key;
  return key.startsWith("plans.saas.quota.") || key === "gateway.saas.events_quota";
}

/** The tone of the subscription state pill. */
export function stateTone(state: string | undefined): "success" | "info" | "warning" | "danger" | "neutral" {
  switch (state) {
    case "active":
      return "success";
    case "trialing":
      return "info";
    case "past_due":
      return "danger";
    case "paused":
      return "warning";
    default:
      return "neutral";
  }
}

/** Whether an invoice of the plan can be paid now. */
export const isPayable = (status: string | undefined): boolean => status === "open" || status === "finalized" || status === "past_due";

/** Parses the text of a limit input: a whole number from 0 up (0 unlimited), or null when it is not one. Empty means "leave as it is" (undefined). */
export function parseLimit(text: string): number | null | undefined {
  const t = text.trim();
  if (t === "") return undefined;
  if (!/^\d{1,15}$/.test(t)) return null;
  return Number(t);
}
