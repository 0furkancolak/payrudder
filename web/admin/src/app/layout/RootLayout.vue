<script setup lang="ts">
import { Link, Outlet, useNavigate } from "@tanstack/vue-router";
import { Menu } from "@lucide/vue";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import PeekHost from "@/features/related/PeekHost.vue";
import VerifyBanner from "@/features/product/VerifyBanner.vue";
import { BrandLogo } from "@/shared/ui/brand-logo";
import { brandName } from "@/shared/lib/brand";
import { privacyMode, setPrivacyMode, sidebarCollapsed } from "@/shared/lib/prefs";
import { isOperator, isPerson, OPERATOR_PATHS } from "@/shared/lib/session";
import PageBoundary from "@/app/errors/PageBoundary.vue";
import { envOf, isEnvironment, rememberEnv } from "@/shared/lib/environment";
import { ALL_APPS, appInSearch, rememberApp } from "@/shared/lib/scope";
import Breadcrumbs from "./Breadcrumbs.vue";
import EnvBanner from "./EnvBanner.vue";
import EnvSwitch from "./EnvSwitch.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import MobileDrawer from "./MobileDrawer.vue";
import PrivacyToggle from "./PrivacyToggle.vue";
import Sidebar from "./Sidebar.vue";
import ThemeToggle from "./ThemeToggle.vue";
import UserMenu from "./UserMenu.vue";
import { updateSearch, useSheetUrlSync } from "@/shared/lib/sheets";
import { TourButton, TourHost } from "@/shared/ui/tour";
import { tourFor, useTourScope } from "../tours";
import { useShell } from "./useShell";

/**
 * The frame of every page. Signed out: a plain header with the brand, the language and the theme. Signed in: the sidebar (open by default on a wide
 * screen, a drawer on a phone or tablet) with the app switcher at its top, and a header with the breadcrumbs, a slot for the tour button, the privacy
 * switch, the sandbox / live switch and the person's menu (which also holds the language and the theme). What the sidebar lists follows the route (see
 * nav.ts) and the scope in the address (`?app=<id|all>`, see shared/lib/scope).
 */
const { t } = useI18n();
const navigate = useNavigate();
const brand = brandName();
// There is one kind of signed-in person; an operator is a person the server also lets into the operator console.
const signedIn = computed(() => isPerson.value);
const shell = useShell();
useSheetUrlSync();
useTourScope();
// The tour icon of the page's header, when the page has a tour (app/tours.ts).
const pageTour = computed(() => (isPerson.value ? tourFor(shell.current.value?.key) : undefined));
const drawer = ref(false);

// A route change closes the drawer (the links also do; this covers the browser's back button).
watch(shell.path, () => (drawer.value = false));

/** The pages that are about one environment's data: the dashboards and the pages of an app. The organisation's own pages (members, settings) are about none. */
const envPage = computed(() => {
  const p = shell.path.value;
  return isPerson.value && !shell.inOperatorArea.value && (p === "/app" || p.startsWith("/app/apps/") || p.startsWith("/app/t/"));
});
/** The environment of the page, once known. */
const pageEnv = computed(() => (envPage.value ? shell.env.value : undefined));
/** The environment of the tenant an operator is looking at (/tenants/<id>), once the tenant has arrived: a sandbox tenant is marked like any sandbox page. */
const operatorTenantEnv = computed(() => {
  const tenant = shell.tenant.data.value;
  return shell.context.value.level === "tenant" && tenant?.environment ? envOf(tenant.environment) : undefined;
});
const sandboxPage = computed(() => (operatorTenantEnv.value ?? pageEnv.value) === "sandbox");

// An app opens next time where the person was: the app and, on a page that names its environment, the environment are remembered.
watch(
  () => shell.scope.value,
  (scope) => {
    if (scope && scope !== ALL_APPS && isPerson.value) rememberApp(scope);
  },
  { immediate: true },
);
watch(
  () => [shell.context.value.appId, shell.context.value.env, shell.located.value, shell.search.value.env] as const,
  ([appId, env, located, searched]) => {
    if (appId && env && isPerson.value && (located || isEnvironment(searched))) rememberEnv(appId, env);
  },
  { immediate: true },
);

/** The pages whose address carries the scope: the dashboards, an app's pages and the organisation's own pages. */
const SCOPED = /^\/app(?:\/(?:apps|t)\/[^/]+.*|\/members|\/settings|\/providers)?$/;
// The address always says which app (or all apps) the page is about: a page that arrives without it, or with another one than its path's, is corrected in place.
watch(
  [shell.scope, shell.path, () => appInSearch(shell.search.value)],
  ([scope, path, inAddress]) => {
    if (!scope || !isPerson.value || shell.inOperatorArea.value || !SCOPED.test(path) || inAddress === scope) return;
    updateSearch((prev) => ({ ...prev, app: scope }));
  },
  { immediate: true },
);


/** What the app switcher needs: the apps and an environment (the switcher is not drawn before the apps have arrived). */
const switcherApps = computed(() => (isPerson.value && !shell.inOperatorArea.value ? shell.apps.data.value?.apps : undefined));
const switcherEnv = computed(() => shell.env.value);

const home = computed(() => (isPerson.value ? "/app" : "/signin"));

// The server's verdict (/auth/me) corrects what this tab believed: somebody who is not an operator, however they got to the console, is sent home.
watch([isOperator, shell.path], () => {
  if (isPerson.value && !isOperator.value && OPERATOR_PATHS.test(shell.path.value)) void navigate({ to: "/app" });
});

// Alt+P turns privacy mode on and off from anywhere (not while typing in a field).
function onKey(e: KeyboardEvent) {
  if (!signedIn.value || !e.altKey || e.ctrlKey || e.metaKey || e.code !== "KeyP") return;
  e.preventDefault();
  setPrivacyMode(!privacyMode.value);
}
onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <div class="min-h-screen">
    <a href="#main" class="bg-primary text-primary-foreground sr-only z-[60] rounded-xl px-4 py-2 focus:not-sr-only focus:fixed focus:start-4 focus:top-4">{{ t("shell.skip") }}</a>

    <template v-if="signedIn">
      <Sidebar :sections="shell.sections.value" :current="shell.current.value" :brand="brand" :home="home" :apps="switcherApps" :scope="shell.scope.value" :env="switcherEnv" :path="shell.path.value" />
      <MobileDrawer v-model:open="drawer" :sections="shell.sections.value" :current="shell.current.value" :brand="brand" :apps="switcherApps" :scope="shell.scope.value" :env="switcherEnv" :path="shell.path.value" />
    </template>

    <!-- One wrapper and one <main> for both states: signing in must not remount the page that is signing in (its onSuccess would be lost). -->
    <div :class="signedIn ? ['transition-[padding] duration-200', sidebarCollapsed ? 'lg:ps-[6.5rem]' : 'lg:ps-[17.5rem]'] : ''">
      <header v-if="signedIn" class="bg-background/85 sticky top-0 z-20 backdrop-blur" data-testid="topbar">
        <div class="mx-auto flex max-w-[1320px] flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 md:px-8 md:py-4">
          <button
            type="button"
            data-testid="menu-button"
            class="bg-card text-foreground border-border/60 shadow-card inline-flex size-10 shrink-0 items-center justify-center rounded-xl border lg:hidden"
            :aria-label="t('shell.openMenu')"
            aria-haspopup="dialog"
            :aria-expanded="drawer"
            @click="drawer = true"
          >
            <Menu class="size-5" aria-hidden="true" />
          </button>
          <Breadcrumbs :crumbs="shell.crumbs.value" class="min-w-0 flex-1" />
          <!-- The tour button of the guided tour (phase 52) mounts here, in the header of every page. -->
          <div id="tour-slot" data-testid="tour-slot" class="flex shrink-0 items-center gap-2"></div>
          <div class="flex shrink-0 items-center gap-2">
            <TourButton v-if="pageTour" :tour="pageTour" />
            <PrivacyToggle />
          </div>
          <EnvSwitch v-if="pageEnv && shell.scope.value" :env="pageEnv" :scope="shell.scope.value" :app="shell.scopeApp.value" :path="shell.path.value" />
          <UserMenu v-if="isPerson" />
        </div>
      </header>
      <header v-else class="mx-auto flex max-w-[1240px] items-center justify-between gap-3 px-4 pt-4 md:px-8 md:pt-6">
        <Link to="/signin" class="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
          <BrandLogo />
        </Link>
        <div class="flex items-center gap-2">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
      <EnvBanner :show="sandboxPage" />
      <main
        id="main"
        tabindex="-1"
        :class="['mx-auto min-w-0 outline-none md:px-8', signedIn ? 'max-w-[1320px] px-4 pt-2 pb-16 md:pt-4' : 'flex max-w-[1240px] flex-col items-center px-4 pt-8 pb-12 md:pt-14']"
      >
        <VerifyBanner v-if="isPerson" />
        <PageBoundary><Outlet /></PageBoundary>
      </main>
      <PeekHost v-if="isPerson" />
      <TourHost v-if="isPerson" />
    </div>
  </div>
</template>
