import { pageTour } from "@/shared/ui/tour";

export const creditsTour = pageTour("credits", "shell.items.credits", [
  { id: "intro", target: "h1" },
  { id: "packs", target: "main h2", title: "tour.common.list" },
  { id: "grant" },
]);
