import { pageTour } from "@/shared/ui/tour";

export const connectTour = pageTour("connect", "shell.items.connect", [
  { id: "intro", target: "h1" },
  { id: "checklist", target: '[data-testid="connect-checklist"]' },
  { id: "code", target: '[data-testid="connect-lang"]' },
]);
