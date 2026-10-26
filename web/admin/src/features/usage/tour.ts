import { pageTour } from "@/shared/ui/tour";

export const usageTour = pageTour("usage", "shell.items.usage", [
  { id: "intro", target: "h1" },
  { id: "tabs", target: '[data-testid^="usage-tab-"]', title: "tour.common.tabs" },
]);
