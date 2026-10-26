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
 *  - "org":      the organisation level of a person: the apps, the members, the settings;
 *  - "app":      one environment of one app (the tenant behind it), for a person: dashboard, customers, packages, subscriptions, invoices, ...;
 *  - "platform": the operator's pages across tenants;
 *  - "tenant":   one tenant for the operator: the same pages as an app plus the operator-only ones (providers, e-invoice, seller address, mail).
 *
 * The sidebar, the mobile drawer and the breadcrumbs are all drawn from what `buildNav` returns, so the three cannot disagree, and the active item
 * follows the route because every item says which paths it owns.
 */
export type Level = "org" | "app" | "platform" | "tenant";

export interface ShellContext {
  level: Level;
  /** The tenant behind the page (app and tenant levels). */
  tenantId?: string;
  /** The app of a person's tenant page, once known. */
  appId?: string;
  /** The environment of a person's app page. */
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
  section: "customers" | "billing" | "growth" | "developers" | "settings";
  person: boolean;
}

const PAGES: Page[] = [
  { key: "customers", icon: Users, path: "customers", section: "customers", person: true },
  { key: "subscriptions", icon: Repeat, path: "subscriptions", section: "customers", person: true },
  { key: "usage", icon: Gauge, path: "usage", section: "customers", person: true },
  { key: "credits", icon: Gift, path: "credits", section: "customers", person: true },
  { key: "meters", icon: Gauge, path: "meters", section: "billing", person: true },
  { key: "packages", icon: Package, path: "packages", section: "billing", person: true },
  { key: "pricing", icon: Percent, path: "pricing", section: "billing", person: true },
  { key: "coupons", icon: Ticket, path: "coupons", section: "billing", person: true },
  { key: "invoices", icon: FileText, path: "invoices", section: "billing", person: true },
  { key: "payments", icon: Wallet, path: "payments", section: "billing", person: true },
  { key: "paymentLinks", icon: Link2, path: "payment-links", section: "billing", person: true },
  { key: "accounting", icon: Receipt, path: "accounting", section: "billing", person: true },
  { key: "reports", icon: BarChart3, path: "reports", section: "billing", person: true },
  { key: "affiliates", icon: Handshake, path: "affiliates", section: "growth", person: true },
  { key: "webhooks", icon: Webhook, path: "webhooks", section: "developers", person: true },
  { key: "apps", icon: KeyRound, path: "apps", section: "developers", person: false },
  { key: "providers", icon: Plug, path: "providers", section: "developers", person: false },
  { key: "mail", icon: Mail, path: "mail", section: "settings", person: true },
  { key: "address", icon: MapPin, path: "address", section: "settings", person: false },
  { key: "einvoice", icon: FileText, path: "e-invoice", section: "settings", person: false },
];

const SECTION_ORDER = ["customers", "billing", "growth", "developers", "settings"] as const;

function item(key: string, to: string, icon: Component, owns: (p: string) => boolean, search?: Record<string, string>): NavItem {
  return { key, label: key, to, icon, owns, ...(search ? { search } : {}) };
}

/** The sections of the sidebar for a place in the console. `has` says which person pages exist (default: PERSON_PAGES). */
export function buildNav(ctx: ShellContext, has: ReadonlySet<string> = new Set<string>(PERSON_PAGES)): NavSection[] {
  switch (ctx.level) {
    case "org":
      return [
        {
          key: "account",
          items: [
            item("overview", "/app", LayoutGrid, exact("/app")),
            ...(has.has("members") ? [item("members", "/app/members", UsersRound, under("/app/members"))] : []),
            ...(ctx.partner ? [item("partner", "/app/partner", Handshake, under("/app/partner"))] : []),
            item("paymentProviders", "/app/providers", Plug, under("/app/providers")),
            item("settings", "/app/settings", Settings, under("/app/settings")),
          ],
        },
        // The operator console is a part of the same app for the people the server lets into it; nobody else sees the door.
        ...(ctx.operator ? [{ key: "platform", items: [item("operator", "/operator", ShieldCheck, exact("/operator"))] }] : []),
      ];
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
    case "app": {
      const tid = ctx.tenantId ?? "";
      const base = `/app/t/${tid}`;
      const dash = ctx.appId ? `/app/apps/${ctx.appId}` : "/app";
      const env = ctx.env ?? "live";
      const sections: NavSection[] = [
        {
          key: "overview",
          items: [
            item("dashboard", dash, LayoutDashboard, exact(dash), { env }),
            // Going live is about the live environment whichever one the page is showing; the link keeps the environment the person is in (a link never flips it).
            ...(ctx.appId ? [item("goLive", `${dash}/go-live`, Rocket, under(`${dash}/go-live`), { env })] : []),
          ],
        },
      ];
      for (const s of SECTION_ORDER) {
        const items = PAGES.filter((p) => p.person && p.section === s && has.has(p.key)).map((p) => item(p.key, `${base}/${p.path}`, p.icon, under(`${base}/${p.path}`)));
        if (s === "developers" && ctx.appId) {
          const keys = `/app/apps/${ctx.appId}/keys`;
          items.unshift(item("apiKeys", keys, KeyRound, under(keys), { env }));
          // The sandbox's tools (reset, scenarios) act on the sandbox only; the page says so, and the link keeps the environment the person is in (a link never flips it).
          const sandbox = `/app/apps/${ctx.appId}/sandbox`;
          items.splice(1, 0, item("sandbox", sandbox, FlaskConical, under(sandbox), { env }));
        }
        if (s === "settings" && ctx.appId) {
          const settings = `/app/apps/${ctx.appId}/settings`;
          items.unshift(item("appSettings", settings, Building2, under(settings), { env }));
          const providers = `/app/apps/${ctx.appId}/providers`;
          items.splice(1, 0, item("paymentProviders", providers, Plug, under(providers), { env }));
        }
        if (items.length > 0) sections.push({ key: s, items });
      }
      return sections;
    }
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
