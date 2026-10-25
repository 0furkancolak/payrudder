import { useRouterState } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useApps, useAccount, useMe, type App } from "@/features/product/api";
import { hasPartnerRole } from "@/features/product/lib";
import { useTenant } from "@/features/tenants/api";
import { envEnum, findTenant, type Environment } from "@/shared/lib/environment";
import { isOperator, isPerson, OPERATOR_PATHS } from "@/shared/lib/session";
import { activeItem, buildNav, type NavSection, type ShellContext } from "./nav";

export interface Crumb {
  label: string;
  to?: string;
  search?: Record<string, string>;
}

/** Finds the app and environment a tenant id belongs to among a person's apps. */
export function appOfTenant(apps: App[] | undefined, tenantId: string): { app: App; env: Environment } | null {
  const hit = findTenant(apps, tenantId);
  return hit ? { app: hit.app, env: hit.env } : null;
}

/**
 * Where the person is, worked out from the route: the level of the console (organisation, app, operator platform, operator tenant), the navigation of
 * that level, which item is current and the breadcrumbs. Everything the sidebar, the drawer and the header show comes from here, so they follow the route.
 */
export function useShell() {
  const { t } = useI18n();
  const location = useRouterState({ select: (s) => s.location });
  const apps = useApps();
  const account = useAccount();
  const me = useMe();
  const partner = computed(() => hasPartnerRole(me.data.value?.memberships));

  const path = computed(() => location.value.pathname.replace(/\/+$/, "") || "/");
  const search = computed(() => (location.value.search ?? {}) as Record<string, unknown>);

  const tenantInPath = computed(() => {
    const m = /^\/(?:app\/t|tenants)\/([^/]+)/.exec(path.value);
    return m?.[1] ?? "";
  });
  const appInPath = computed(() => /^\/app\/apps\/([^/]+)/.exec(path.value)?.[1] ?? "");

  // An operator is a person: the console's pages are theirs only while they are on them, and their own organisation is one click away.
  const inOperatorArea = computed(() => isOperator.value && OPERATOR_PATHS.test(path.value));
  const operatorTenant = computed(() => (inOperatorArea.value && path.value.startsWith("/tenants/") ? tenantInPath.value : ""));
  const tenant = useTenant(operatorTenant);

  const located = computed(() => (isPerson.value && path.value.startsWith("/app/t/") ? appOfTenant(apps.data.value?.apps, tenantInPath.value) : null));

  const context = computed<ShellContext>(() => {
    if (inOperatorArea.value) {
      return operatorTenant.value ? { level: "tenant", tenantId: operatorTenant.value } : { level: "platform" };
    }
    if (appInPath.value) {
      const app = apps.data.value?.apps?.find((a) => a.id === appInPath.value);
      const envSearch = search.value.env === "sandbox" ? "sandbox" : "live";
      const tenantId = app?.tenants?.find((x) => x.environment === envEnum(envSearch))?.id;
      return { level: "app", appId: appInPath.value, env: envSearch, operator: isOperator.value, ...(tenantId ? { tenantId } : {}) };
    }
    if (path.value.startsWith("/app/t/")) {
      return { level: "app", tenantId: tenantInPath.value, ...(located.value ? { appId: located.value.app.id ?? "", env: located.value.env } : {}) };
    }
    return { level: "org", operator: isOperator.value, partner: partner.value };
  });

  const sections = computed<NavSection[]>(() => buildNav(context.value));
  const current = computed(() => activeItem(sections.value, path.value));

  const appName = computed(() => {
    const id = context.value.appId;
    return apps.data.value?.apps?.find((a) => a.id === id)?.name ?? "";
  });
  const orgName = computed(() => account.data.value?.name ?? "");

  const crumbs = computed<Crumb[]>(() => {
    const list: Crumb[] = [];
    const ctx = context.value;
    const section = current.value ? { label: t(`shell.items.${current.value.label}`) } : null;
    if (ctx.level === "platform") {
      list.push({ label: t("shell.platform"), to: "/tenants" });
      if (section) list.push(section);
    } else if (ctx.level === "tenant") {
      list.push({ label: t("shell.platform"), to: "/tenants" });
      list.push({ label: tenant.data.value?.name ?? t("tenant.fallbackTitle"), to: `/tenants/${ctx.tenantId}` });
      if (section && current.value?.key !== "dashboard") list.push(section);
    } else if (ctx.level === "app") {
      list.push({ label: orgName.value || t("shell.items.overview"), to: "/app" });
      list.push({ label: appName.value || "…", ...(ctx.appId ? { to: `/app/apps/${ctx.appId}`, search: { env: ctx.env ?? "live" } } : {}) });
      if (section && current.value?.key !== "dashboard") list.push(section);
    } else {
      list.push({ label: orgName.value || t("shell.org"), to: "/app" });
      if (path.value.startsWith("/app/partner")) {
        // The partner portal is the person's own, whether or not the sidebar lists it (the apply page is open to everybody signed in).
        list.push({ label: t("shell.items.partner"), ...(path.value === "/app/partner" ? {} : { to: "/app/partner" }) });
      } else if (section && current.value?.key !== "overview") list.push(section);
    }
    return list;
  });

  return { path, context, sections, current, crumbs, apps, appName, orgName, located, tenant };
}
