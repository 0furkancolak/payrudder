import { FIND_STEP, pageTour } from "@/shared/ui/tour";

export const packagesTour = pageTour("packages", "shell.items.packages", [
  { id: "intro", target: "h1" },
  FIND_STEP,
  { id: "create", target: '[data-testid="packages-new"]', title: "tour.common.create" },
  { id: "trials" },
]);
