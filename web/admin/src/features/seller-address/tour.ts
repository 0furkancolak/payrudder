import { pageTour } from "@/shared/ui/tour";

/** The address printed as the seller on every invoice. */
export const addressTour = pageTour("address", "shell.items.address", [
  { id: "intro", target: "h1" },
  { id: "preview", target: '[data-testid="invoice-preview"]', optional: true },
]);
