import { pageTour } from "@/shared/ui/tour";

/** The operator's page of a tenant's apps. */
export const appsTour = pageTour("apps", "shell.items.apps", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="apps-new"]', title: "tour.common.create", optional: true },
]);
