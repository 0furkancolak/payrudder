import { pageTour } from "@/shared/ui/tour";

export const contractsTour = pageTour("contracts", "shell.items.contracts", [
  { id: "intro", target: "h1" },
  { id: "agreements", target: '[data-testid="contracts-agreements"]' },
  { id: "settings", target: '[data-testid="contracts-tab-settings"]' },
]);
