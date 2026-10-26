import { pageTour } from "@/shared/ui/tour";

export const metersTour = pageTour("meters", "shell.items.meters", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="meters-new"]', title: "tour.common.create" },
]);
