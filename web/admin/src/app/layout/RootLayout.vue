<script setup lang="ts">
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/vue-router";
import { FlaskConical, Inbox, LayoutGrid, Settings, ShieldCheck, Users } from "@lucide/vue";
import { computed, watch } from "vue";
import { useI18n } from "vue-i18n";
import { brandName } from "@/shared/lib/brand";
import { isOperator, isPerson } from "@/shared/lib/session";
import VerifyBanner from "@/features/product/VerifyBanner.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import Rail from "./Rail.vue";
import ThemeToggle from "./ThemeToggle.vue";
import UserMenu from "./UserMenu.vue";

const { t } = useI18n();
const brand = brandName();
const path = useRouterState({ select: (s) => s.location.pathname });

const signedIn = computed(() => isPerson.value);
const OPERATOR_PATHS = /^\/(operator|tenants|playground|dead-letters)(\/|$)/;
/** The operator console is a part of the app only operators see (the server decides again on every request); everything else is a person's own. */
const inOperatorArea = computed(() => isOperator.value && OPERATOR_PATHS.test(path.value));

// The server's verdict (/auth/me) corrects what this tab believed: somebody who is not an operator, however they got to the console, is sent home.
const navigate = useNavigate();
watch([isOperator, path], () => {
  if (isPerson.value && !isOperator.value && OPERATOR_PATHS.test(path.value)) void navigate({ to: "/app" });
});

// The rail follows where the person is: their organisation and its settings (plus a door to the operator console for operators), or the platform's pages.
const items = computed(() =>
  inOperatorArea.value
    ? [
        { to: "/tenants", label: t("nav.tenants"), icon: Users },
        { to: "/playground", label: t("nav.playground"), icon: FlaskConical },
        { to: "/dead-letters", label: t("nav.deadLetters"), icon: Inbox },
        { to: "/app", label: t("nav.myApps"), icon: LayoutGrid, exact: true },
      ]
    : [
        { to: "/app", label: t("product.nav.home"), icon: LayoutGrid, exact: true },
        { to: "/app/settings", label: t("product.nav.settings"), icon: Settings },
        ...(isOperator.value ? [{ to: "/operator", label: t("nav.operator"), icon: ShieldCheck }] : []),
      ],
);
</script>

<template>
  <div class="min-h-screen">
    <Rail v-if="signedIn" :items="items" :brand="brand" />
    <div :class="signedIn ? 'md:ps-[7rem]' : ''">
      <header class="mx-auto flex max-w-[1240px] items-center justify-between gap-3 px-4 pt-4 md:px-8 md:pt-6">
        <Link :to="isPerson ? '/app' : '/signin'" class="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
          <span v-if="!signedIn" class="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-xl text-base font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
          <span :class="signedIn && 'md:sr-only'">{{ brand }}</span>
        </Link>
        <div class="flex items-center gap-2">
          <LanguageSwitcher />
          <ThemeToggle />
          <UserMenu v-if="isPerson" />
        </div>
      </header>
      <main :class="['mx-auto max-w-[1240px] px-4 pt-6 pb-32 md:px-8 md:pb-12', !signedIn && 'flex flex-col items-center pt-8 md:pt-14']"><VerifyBanner v-if="isPerson" /><Outlet /></main>
    </div>
  </div>
</template>
