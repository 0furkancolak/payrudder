import { pageTour } from "@/shared/ui/tour";

/** The operator's page of a tenant's e-invoice accounts and settings. */
export const einvoiceTour = pageTour("einvoice", "shell.items.einvoice", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list", optional: true },
]);
