type Op = "create" | "unchanged" | "conflict" | "skipped";

interface Change {
  family?: string;
  key?: string;
  action?: string;
  detail?: string;
  error?: string;
}

interface Result {
  created?: number;
  unchanged?: number;
  conflicts?: number;
  skipped?: number;
  failed?: number;
  changes?: Change[];
}

/** The API's action word without its prefix ("ACTION_CREATE" -> "create"); a word the console does not know reads as "unchanged", never as a write. */
export function actionOf(a: string | undefined): Op {
  switch (a) {
    case "ACTION_CREATE":
      return "create";
    case "ACTION_CONFLICT":
      return "conflict";
    case "ACTION_SKIPPED":
      return "skipped";
    default:
      return "unchanged";
  }
}

const FAMILIES = ["meters", "plans", "taxes", "coupons", "mail", "webhooks"];

/**
 * What the promote card shows: the counts, and the rows worth a line (what would be created, what conflicts, what was skipped or failed; the objects that
 * are already the same are counted, not listed). `nothing` is true when there is nothing to copy and nothing to say.
 */
export function promoteSummary(r: Result | undefined) {
  const rows = (r?.changes ?? [])
    .map((c: Change) => ({ family: FAMILIES.includes(c.family ?? "") ? (c.family as string) : "meters", key: c.key ?? "", action: actionOf(c.action), detail: c.detail ?? "", error: c.error ?? "" }))
    .filter((c) => c.action !== "unchanged" || c.error !== "");
  const created = r?.created ?? 0;
  const unchanged = r?.unchanged ?? 0;
  const conflicts = r?.conflicts ?? 0;
  const skipped = r?.skipped ?? 0;
  const failed = r?.failed ?? 0;
  return { created, unchanged, conflicts, skipped, failed, rows, nothing: rows.length === 0 };
}
