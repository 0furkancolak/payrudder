import { pageTour } from "@/shared/ui/tour";

/** The person's help page, and the operator's inbox (a platform page). */
export const supportTour = pageTour("support", "shell.items.support", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="support-new"]', title: "tour.common.create" },
]);

export const supportInboxTour = pageTour("supportInbox", "shell.items.supportInbox", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list", optional: true },
]);
