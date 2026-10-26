import { FIND_STEP, pageTour } from "@/shared/ui/tour";

export const customersTour = pageTour("customers", "shell.items.customers", [
  { id: "intro", target: "h1" },
  FIND_STEP,
  { id: "create", target: '[data-testid="customers-new"]', title: "tour.common.create" },
]);
