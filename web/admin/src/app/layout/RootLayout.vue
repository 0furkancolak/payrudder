<script setup lang="ts">
import { Link, Outlet, useNavigate } from "@tanstack/vue-router";
import { useQueryClient } from "@tanstack/vue-query";
import { LogOut, Menu } from "@lucide/vue";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import VerifyBanner from "@/features/product/VerifyBanner.vue";
import { brandName } from "@/shared/lib/brand";
import { privacyMode, setPrivacyMode, sidebarCollapsed } from "@/shared/lib/prefs";
import { isOperator, isPerson, setOperatorToken } from "@/shared/lib/session";
import Breadcrumbs from "./Breadcrumbs.vue";
import ContextSwitcher from "./ContextSwitcher.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import MobileDrawer from "./MobileDrawer.vue";
import PrivacyToggle from "./PrivacyToggle.vue";
import Sidebar from "./Sidebar.vue";
import ThemeToggle from "./ThemeToggle.vue";
import UserMenu from "./UserMenu.vue";
import { useShell } from "./useShell";

/**
 * The frame of every page. Signed out: a plain header with the brand, the language and the theme. Signed in: the sidebar (open by default on a wide
 * screen, a drawer on a phone or tablet), and a header with the breadcrumbs, the app and environment switchers, the privacy switch, the language, the
 * theme and the person's menu. What the sidebar lists follows the route (see nav.ts).
 */
const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();
const brand = brandName();
const signedIn = computed(() => isPerson.value || isOperator.value);
const shell = useShell();
const drawer = ref(false);
const menuButton = ref<HTMLButtonElement | null>(null);

// A route change closes the drawer (the links also do; this covers the browser's back button).
watch(shell.path, () => (drawer.value = false));

const home = computed(() => (isPerson.value ? "/app" : isOperator.value ? "/tenants" : "/signin"));

async function operatorSignOut() {
  drawer.value = false;
  setOperatorToken(null);
  queries.clear();
  await navigate({ to: "/operator" });
}

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
      <Sidebar :sections="shell.sections.value" :current="shell.current.value" :brand="brand" :home="home" @sign-out="operatorSignOut" />
      <MobileDrawer v-model:open="drawer" :sections="shell.sections.value" :current="shell.current.value" :brand="brand" @sign-out="operatorSignOut" />
    </template>

    <!-- One wrapper and one <main> for both states: signing in must not remount the page that is signing in (its onSuccess would be lost). -->
    <div :class="signedIn ? ['transition-[padding] duration-200', sidebarCollapsed ? 'lg:ps-[6.5rem]' : 'lg:ps-[17.5rem]'] : ''">
      <header v-if="signedIn" class="bg-background/85 sticky top-0 z-20 backdrop-blur" data-testid="topbar">
        <div class="mx-auto flex max-w-[1320px] flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 md:px-8 md:py-4">
          <button
            ref="menuButton"
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
          <div class="flex shrink-0 items-center gap-2">
            <PrivacyToggle />
            <LanguageSwitcher />
            <ThemeToggle />
            <UserMenu v-if="isPerson" />
            <button
              v-if="isOperator"
              type="button"
              class="bg-card text-muted-foreground hover:text-foreground border-border/60 shadow-card inline-flex size-10 items-center justify-center rounded-xl border"
              :aria-label="t('nav.signOut')"
              :title="t('nav.signOut')"
              @click="operatorSignOut"
            >
              <LogOut class="size-4" />
            </button>
          </div>
          <ContextSwitcher
            v-if="isPerson && shell.context.value.level === 'app'"
            class="order-last w-full sm:order-none sm:w-auto"
            :context="shell.context.value"
            :apps="shell.apps.data.value?.apps ?? []"
            :app-name="shell.appName.value"
            :path="shell.path.value"
          />
        </div>
      </header>
      <header v-else class="mx-auto flex max-w-[1240px] items-center justify-between gap-3 px-4 pt-4 md:px-8 md:pt-6">
        <Link to="/signin" class="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
          <span class="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-xl text-base font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
          <span>{{ brand }}</span>
        </Link>
        <div class="flex items-center gap-2">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
      <main
        id="main"
        tabindex="-1"
        :class="['mx-auto min-w-0 outline-none md:px-8', signedIn ? 'max-w-[1320px] px-4 pt-2 pb-16 md:pt-4' : 'flex max-w-[1240px] flex-col items-center px-4 pt-8 pb-12 md:pt-14']"
      >
        <VerifyBanner v-if="isPerson" />
        <Outlet />
      </main>
    </div>
  </div>
</template>
