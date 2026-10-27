import {
  BarChart3,
  Building2,
  FileText,
  FlaskConical,
  Gauge,
  Gift,
  Handshake,
  Inbox,
  KeyRound,
  LayoutDashboard,
  LayoutGrid,
  Link2,
  Mail,
  MapPin,
  Package,
  Percent,
  Plug,
  Receipt,
  Repeat,
  Rocket,
  Settings,
  ShieldCheck,
  Ticket,
  Users,
  UsersRound,
  Wallet,
  Webhook,
} from "@lucide/vue";
import type { Component } from "vue";
import type { Environment } from "@/shared/lib/environment";

/**
 * The navigation of the signed-in console, as data. It depends on where the person is:
 *
 *  - "org" and "app": the same sidebar for a person, because an app is always selected: the selected app's pages in categories (overview, customers and
 *    billing, usage and products, money, developers, app settings) and, at the bottom of that group, the organisation's own pages (members, payment
 *    providers, settings). With the scope "all" the pages are the same ones over every app (the pages that belong to one app are not offered);
 *  - "platform": the operator's pages across tenants;
 *  - "tenant":   one tenant for the operator: the same pages as an app plus the operator-only ones (providers, e-invoice, seller address, mail).
 *
 * The sidebar, the mobile drawer and the breadcrumbs are all drawn from what `buildNav` returns, so the three cannot disagree, and the active item
 * follows the route because every item says which paths it owns.
 */
export type Level = "org" | "app" | "platform" | "tenant";

export interface ShellContext {
  level: Level;
  /** The tenant behind the page: the selected app's tenant in the environment (app and tenant levels). */
  tenantId?: string;
  /** The selected app, once known (never set while the scope is "all"). */
  appId?: string;
  /** The scope: the selected app's id, or "all" for every app (see shared/lib/scope). */
  scope?: string;
  /** The environment the pages show. */
  env?: Environment;
  /** Whether the server says this person is a platform operator: the organisation level then has a door to the operator console. */
  operator?: boolean;
  /** Whether the person is a partner of a seller (a membership with the role affiliate): the organisation level then has the partner portal. */
  partner?: boolean;
}

export interface NavItem {
  key: string;
  /** The i18n key under `shell.items`. */
  label: string;
  to: string;
  search?: Record<string, string>;
  icon: Component;
  /** Whether a path belongs to this item (the item is then current). */
  owns: (path: string) => boolean;
}

export interface NavSection {
  key: string;
  items: NavItem[];
}

const under = (base: string) => (path: string) => path === base || path.startsWith(`${base}/`);
const exact = (to: string) => (path: string) => path === to;

/**
 * The pages a person has under /app/t/{tenant}/... (route file: features/product/routes.ts). A page is in the sidebar only when its route exists, so
 * nothing in the navigation leads to a "not found". Add a key here in the same change that adds the route.
 */
export const PERSON_PAGES = ["customers", "subscriptions", "usage", "credits", "meters", "packages", "pricing", "coupons", "invoices", "payments", "paymentLinks", "accounting", "reports", "affiliates", "webhooks", "mail", "members"] as const;

/** The pages of a tenant: key, icon, path under the tenant, the section it sits in, and whether a person has it or only the operator. */
interface Page {
  key: string;
  icon: Component;
  path: string;
  section: "customersBilling" | "usageProducts" | "money" | "developers" | "settings";
  person: boolean;
}

const PAGES: Page[] = [
  { key: "customers", icon: Users, path: "customers", section: "customersBilling", person: true },
  { key: "subscriptions", icon: Repeat, path: "subscriptions", section: "customersBilling", person: true },
  { key: "invoices", icon: FileText, path: "invoices", section: "customersBilling", person: true },
  { key: "payments", icon: Wallet, path: "payments", section: "customersBilling", person: true },
  { key: "paymentLinks", icon: Link2, path: "payment-links", section: "customersBilling", person: true },
  { key: "credits", icon: Gift, path: "credits", section: "customersBilling", person: true },
  { key: "usage", icon: Gauge, path: "usage", section: "usageProducts", person: true },
  { key: "meters", icon: Gauge, path: "meters", section: "usageProducts", person: true },
  { key: "packages", icon: Package, path: "packages", section: "usageProducts", person: true },
  { key: "pricing", icon: Percent, path: "pricing", section: "usageProducts", person: true },
  { key: "coupons", icon: Ticket, path: "coupons", section: "usageProducts", person: true },
  { key: "accounting", icon: Receipt, path: "accounting", section: "money", person: true },
  { key: "reports", icon: BarChart3, path: "reports", section: "money", person: true },
  { key: "affiliates", icon: Handshake, path: "affiliates", section: "money", person: true },
  { key: "webhooks", icon: Webhook, path: "webhooks", section: "developers", person: true },
  { key: "apps", icon: KeyRound, path: "apps", section: "developers", person: false },
  { key: "providers", icon: Plug, path: "providers", section: "developers", person: false },
  { key: "mail", icon: Mail, path: "mail", section: "settings", person: true },
  { key: "address", icon: MapPin, path: "address", section: "settings", person: false },
  { key: "einvoice", icon: FileText, path: "e-invoice", section: "settings", person: false },
];

const SECTION_ORDER = ["customersBilling", "usageProducts", "money", "developers", "settings"] as const;

function item(key: string, to: string, icon: Component, owns: (p: string) => boolean, search?: Record<string, string>): NavItem {
  return { key, label: key, to, icon, owns, ...(search ? { search } : {}) };
}

/** The tenant id the pages under /app/t/ carry for "every app" (the layout then lists the apps' own data side by side). */
export const ALL_TENANTS = "all";

/**
 * The sidebar of a person: the selected app's pages in categories, then the organisation's own pages at the bottom of the same group, then (for an
 * operator only) the way into the operator console. Every link carries the scope (and the environment) so the address says what the page is about.
 */
function personNav(ctx: ShellContext, has: ReadonlySet<string>): NavSection[] {
  const all = ctx.scope === "all";
  const env = ctx.env ?? "live";
  const scope = ctx.scope ?? ctx.appId;
  const withScope = (extra: Record<string, string> = {}): Record<string, string> => ({ ...(scope ? { app: scope } : {}), ...extra });
  const base = all ? `/app/t/${ALL_TENANTS}` : ctx.tenantId ? `/app/t/${ctx.tenantId}` : "";
  const tenantSearch = withScope(all ? { env } : {});
  const sections: NavSection[] = [];

  if (all) {
    sections.push({ key: "overview", items: [item("dashboard", "/app", LayoutDashboard, exact("/app"), withScope({ env }))] });
  } else if (ctx.appId) {
    const dash = `/app/apps/${ctx.appId}`;
    // Going live is about the live environment whichever one the page is showing; the link keeps the environment the person is in (a link never flips it).
    sections.push({
      key: "overview",
      items: [item("dashboard", dash, LayoutDashboard, exact(dash), withScope({ env })), item("goLive", `${dash}/go-live`, Rocket, under(`${dash}/go-live`), withScope({ env }))],
    });
  }

  for (const s of SECTION_ORDER) {
    const items = base ? PAGES.filter((p) => p.person && p.section === s && has.has(p.key)).map((p) => item(p.key, `${base}/${p.path}`, p.icon, under(`${base}/${p.path}`), tenantSearch)) : [];
    if (ctx.appId && !all) {
      const app = `/app/apps/${ctx.appId}`;
      const appSearch = withScope({ env });
      if (s === "developers") {
        items.unshift(item("apiKeys", `${app}/keys`, KeyRound, under(`${app}/keys`), appSearch));
        // The sandbox's tools (reset, scenarios) act on the sandbox only; the page says so, and the link keeps the environment the person is in.
        items.push(item("sandbox", `${app}/sandbox`, FlaskConical, under(`${app}/sandbox`), appSearch));
      }
      if (s === "settings") {
        items.unshift(item("appSettings", `${app}/settings`, Building2, under(`${app}/settings`), appSearch), item("paymentProviders", `${app}/providers`, Plug, under(`${app}/providers`), appSearch));
      }
    }
    if (items.length > 0) sections.push({ key: s, items });
  }

  // The organisation's own pages close the app group.
  const org = withScope(ctx.env ? { env: ctx.env } : {});
  sections.push({
    key: "account",
    items: [
      ...(has.has("members") ? [item("members", "/app/members", UsersRound, under("/app/members"), org)] : []),
      ...(ctx.partner ? [item("partner", "/app/partner", Handshake, under("/app/partner"), org)] : []),
      item("orgProviders", "/app/providers", Plug, under("/app/providers"), org),
      item("settings", "/app/settings", Settings, under("/app/settings"), org),
    ],
  });
  // The operator console is a part of the same app for the people the server lets into it; nobody else sees the door. It comes last.
  if (ctx.operator) sections.push({ key: "platform", items: [item("operator", "/operator", ShieldCheck, exact("/operator"))] });
  return sections;
}

/** The sections of the sidebar for a place in the console. `has` says which person pages exist (default: PERSON_PAGES). */
export function buildNav(ctx: ShellContext, has: ReadonlySet<string> = new Set<string>(PERSON_PAGES)): NavSection[] {
  switch (ctx.level) {
    case "org":
    case "app":
      return personNav(ctx, has);
    case "platform":
      return [
        {
          key: "platform",
          items: [
            item("tenants", "/tenants", Users, (p) => p === "/tenants"),
            item("playground", "/playground", FlaskConical, under("/playground")),
            item("deadLetters", "/dead-letters", Inbox, under("/dead-letters")),
          ],
        },
        { key: "account", items: [item("myApps", "/app", LayoutGrid, exact("/app"))] },
      ];
    case "tenant": {
      const base = `/tenants/${ctx.tenantId ?? ""}`;
      const sections: NavSection[] = [
        { key: "overview", items: [item("dashboard", base, LayoutDashboard, exact(base))] },
      ];
      for (const s of SECTION_ORDER) {
        const items = PAGES.filter((p) => p.section === s).map((p) => item(p.key, `${base}/${p.path}`, p.icon, under(`${base}/${p.path}`)));
        if (items.length > 0) sections.push({ key: s, items });
      }
      sections.push({ key: "account", items: [item("myApps", "/app", LayoutGrid, exact("/app"))] });
      return sections;
    }
  }
}

/** The item whose paths contain `path`: the longest `to` among the owners, so a deeper page wins over its parent. */
export function activeItem(sections: NavSection[], path: string): NavItem | undefined {
  let best: NavItem | undefined;
  for (const s of sections) {
    for (const it of s.items) {
      if (it.owns(path) && (!best || it.to.length > best.to.length)) best = it;
    }
  }
  return best;
}
