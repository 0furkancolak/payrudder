import { pageTour } from "@/shared/ui/tour";

export const mailTour = pageTour("mail", "shell.items.mail", [
  { id: "intro", target: "h1" },
  { id: "categories", target: '[data-testid^="mail-"]' },
]);
