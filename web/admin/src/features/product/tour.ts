import { pageTour } from "@/shared/ui/tour";

/**
 * The tours of the pages that belong to the product itself (the organisation's home, an app's own pages, the members, the settings, invoices and payments).
 * The home's starts by itself on the first visit; the others wait for the tour icon. Targets are the pieces of the frame that already carry a test id.
 */
export const homeTour = pageTour("overview", "tour.pages.overview.welcomeTitle", [
  { id: "welcome" },
  { id: "apps", target: '[data-testid="sidebar-apps"]', optional: true },
  { id: "groups", target: '[data-testid="sidebar-nav"]', optional: true },
  { id: "sandbox", target: '[data-testid="env-switch"]', optional: true },
  { id: "account", target: '[data-testid="user-menu"]' },
  { id: "again", target: '[data-testid="tour-button"]', optional: true },
]);

export const dashboardTour = pageTour("dashboard", "shell.items.dashboard", [
  { id: "intro", target: "h1" },
  { id: "figures", target: '[data-testid="dashboard"]' },
]);

export const goLiveTour = pageTour("goLive", "shell.items.goLive", [
  { id: "intro", target: "h1" },
  { id: "checklist", target: "main h2" },
]);

export const apiKeysTour = pageTour("apiKeys", "shell.items.apiKeys", [
  { id: "intro", target: "h1" },
  { id: "create", target: '[data-testid="new-sandbox-key"]', title: "tour.common.create" },
]);

export const sandboxTour = pageTour("sandbox", "shell.items.sandbox", [
  { id: "intro", target: "h1" },
  { id: "scenarios", target: '[data-testid="scenarios"]' },
]);

export const appSettingsTour = pageTour("appSettings", "shell.items.appSettings", [{ id: "intro", target: "h1" }]);

export const membersTour = pageTour("members", "shell.items.members", [
  { id: "intro", target: "h1" },
  { id: "invite", target: '[data-testid="invite-open"]', title: "tour.common.create" },
]);

export const settingsTour = pageTour("settings", "shell.items.settings", [{ id: "intro", target: "h1" }]);

export const invoicesTour = pageTour("invoices", "shell.items.invoices", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list" },
]);

export const paymentsTour = pageTour("payments", "shell.items.payments", [
  { id: "intro", target: "h1" },
  { id: "list", target: "main table", title: "tour.common.list" },
]);
