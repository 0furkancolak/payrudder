import { computed } from "vue";
import type { NavSection } from "@/app/layout/nav";
import { useMySupportSummary, useSupportSummary } from "./api";

/**
 * The counts the sidebar shows beside the help pages: for a person, their tickets with an answer they have not read ("support"); for an operator, the tickets
 * waiting for an answer and how many of those are past their deadline ("supportInbox"). Each is asked only when the sidebar has that page, so nothing is read
 * for a person who has no help page or an operator's inbox they cannot reach.
 */
export function useNavBadges(sections: () => NavSection[]) {
  const has = (key: string) => sections().some((s) => s.items.some((i) => i.key === key));
  const mine = useMySupportSummary(() => has("support"));
  const ops = useSupportSummary(() => has("supportInbox"));
  return computed<Record<string, { count: number; late: number }>>(() => ({
    support: { count: mine.data.value?.unread ?? 0, late: 0 },
    supportInbox: { count: ops.data.value?.waiting ?? 0, late: ops.data.value?.sla_breached ?? 0 },
  }));
}
