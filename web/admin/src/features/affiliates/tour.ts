import { pageTour } from "@/shared/ui/tour";

export const affiliatesTour = pageTour("affiliates", "shell.items.affiliates", [
  { id: "intro", target: "h1" },
  { id: "tabs", target: 'main [role="tablist"]', title: "tour.common.tabs" },
]);
