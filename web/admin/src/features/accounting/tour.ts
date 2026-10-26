import { pageTour } from "@/shared/ui/tour";

export const accountingTour = pageTour("accounting", "shell.items.accounting", [
  { id: "intro", target: "h1" },
  { id: "currencies" },
]);
