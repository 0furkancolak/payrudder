import { pageTour } from "@/shared/ui/tour";

export const webhooksTour = pageTour("webhooks", "shell.items.webhooks", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="webhooks-new"]', title: "tour.common.create" },
]);
