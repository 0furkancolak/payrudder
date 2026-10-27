import { pageTour } from "@/shared/ui/tour";

export const providersTour = pageTour("paymentProviders", "shell.items.integrations", [
  { id: "intro", target: "h1" },
  { id: "cards", target: '[data-testid^="integration-card-"]' },
  { id: "sandbox" },
]);
