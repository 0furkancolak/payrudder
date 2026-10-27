import { pageTour } from "@/shared/ui/tour";

export const creditsTour = pageTour("credits", "shell.items.credits", [
  { id: "intro", target: "h1" },
  { id: "grant", target: '[data-testid="credits-balance"]' },
  { id: "packs", target: '[data-testid="credits-sell"]', title: "tour.common.list" },
]);
