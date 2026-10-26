import { pageTour } from "@/shared/ui/tour";

export const paymentLinksTour = pageTour("paymentLinks", "shell.items.paymentLinks", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list" },
]);
