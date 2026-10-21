export { default as StatusPill } from "./StatusPill.vue";

export type Tone = "success" | "info" | "warning" | "danger" | "neutral";

/** The tone of a payment, invoice or app status word. Unknown words are neutral. */
export function toneOf(status: string | undefined): Tone {
  switch (status) {
    case "paid":
    case "ok":
    case "active":
      return "success";
    case "open":
      return "info";
    case "processing":
    case "pending":
    case "attention":
    case "refunded":
      return "warning";
    case "failed":
    case "uncollectible":
    case "degraded":
    case "void":
    case "revoked":
      return "danger";
    default:
      return "neutral";
  }
}
