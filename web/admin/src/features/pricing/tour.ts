import { pageTour } from "@/shared/ui/tour";

export const pricingTour = pageTour("pricing", "shell.items.pricing", [
  { id: "intro", target: "h1" },
  { id: "tabs", target: 'main [role="tablist"]', title: "tour.common.tabs" },
]);

export const couponsTour = pageTour("coupons", "shell.items.coupons", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="coupons-new"]', title: "tour.common.create" },
]);
