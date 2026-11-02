import { pageTour } from "@/shared/ui/tour";

/** The organisation's plan and billing (the SaaS), and the operator's list of organisations with their plans. */
export const planTour = pageTour("plan", "shell.items.plan", [
  { id: "intro", target: "h1" },
  { id: "usage", target: '[data-testid="plan-usage"]', optional: true },
]);

export const organisationsTour = pageTour("organisations", "shell.items.organisations", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list", optional: true },
]);
