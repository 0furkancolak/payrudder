export { default as StatusPill } from "./StatusPill.vue";

export type Tone = "success" | "info" | "warning" | "danger" | "neutral";

/**
 * The tone of a payment, invoice or app status word, as payrudder-brand/DESIGN.md section 3 lays them out: succeeded/paid/settled are success;
 * pending/processing/requires_action are warning; failed/declined/disputed are destructive; refunded/void/draft are muted; routed/retrying are brand.
 * Words of the console that the table does not name (an app's health, an open invoice) keep their own tone. Unknown words are neutral.
 */
export function toneOf(status: string | undefined): Tone {
  switch (status) {
    case "succeeded":
    case "paid":
    case "settled":
    case "ok":
    case "active":
      return "success";
    case "open":
    case "routed":
    case "retrying":
      return "info";
    case "pending":
    case "processing":
    case "requires_action":
    case "attention":
      return "warning";
    case "failed":
    case "declined":
    case "disputed":
    case "uncollectible":
    case "degraded":
    case "revoked":
      return "danger";
    case "refunded":
    case "void":
    case "draft":
    default:
      return "neutral";
  }
}
