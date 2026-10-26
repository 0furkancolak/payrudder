import { pageTour } from "@/shared/ui/tour";

export const reportsTour = pageTour("reports", "shell.items.reports", [
  { id: "intro", target: "h1" },
  { id: "table", target: "main table", title: "tour.common.list" },
]);
