import { pageTour } from "@/shared/ui/tour";

export const providersTour = pageTour("paymentProviders", "shell.items.paymentProviders", [
  { id: "intro", target: "h1" },
  { id: "cards", target: '[data-testid^="provider-card-"]' },
  { id: "sandbox" },
]);
