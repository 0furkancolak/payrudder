import { pageTour } from "@/shared/ui/tour";

/** The operator console's own pages (the platform level). A tenant's pages have the same tours as a person's. */
export const tenantsTour = pageTour("tenants", "shell.items.tenants", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list" },
]);
