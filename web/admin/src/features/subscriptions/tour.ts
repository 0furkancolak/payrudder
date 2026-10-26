import { FIND_STEP, pageTour } from "@/shared/ui/tour";

export const subscriptionsTour = pageTour("subscriptions", "shell.items.subscriptions", [
  { id: "intro", target: "h1" },
  FIND_STEP,
  { id: "create", target: '[data-testid="subscriptions-new"]', title: "tour.common.create" },
]);
