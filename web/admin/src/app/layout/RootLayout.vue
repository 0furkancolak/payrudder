<script setup lang="ts">
import { Link, Outlet, useNavigate } from "@tanstack/vue-router";
import { useQueryClient } from "@tanstack/vue-query";
import { FlaskConical, Inbox, LayoutGrid, LogOut, Settings, Users } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { brandName } from "@/shared/lib/brand";
import { isOperator, isPerson, setOperatorToken } from "@/shared/lib/session";
import VerifyBanner from "@/features/product/VerifyBanner.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import Rail from "./Rail.vue";
import ThemeToggle from "./ThemeToggle.vue";
import UserMenu from "./UserMenu.vue";

const { t } = useI18n();
const navigate = useNavigate();
const queries = useQueryClient();
const brand = brandName();

const signedIn = computed(() => isPerson.value || isOperator.value);

// The rail follows who is signed in: a person has their organisation and its settings, the operator has the platform's pages.
const items = computed(() =>
  isOperator.value
    ? [
        { to: "/tenants", label: t("nav.tenants"), icon: Users },
        { to: "/playground", label: t("nav.playground"), icon: FlaskConical },
        { to: "/dead-letters", label: t("nav.deadLetters"), icon: Inbox },
      ]
    : [
        { to: "/app", label: t("product.nav.home"), icon: LayoutGrid, exact: true },
        { to: "/app/settings", label: t("product.nav.settings"), icon: Settings },
      ],
);

async function operatorSignOut() {
  setOperatorToken(null);
  queries.clear();
  await navigate({ to: "/operator" });
}
</script>

<template>
  <div class="min-h-screen">
    <Rail v-if="signedIn" :items="items" :brand="brand">
      <template v-if="isOperator" #bottom>
        <button
          type="button"
          class="text-rail-foreground relative flex size-11 items-center justify-center rounded-2xl transition-colors hover:bg-white/10 hover:text-white"
          :title="t('nav.signOut')"
          @click="operatorSignOut"
        >
          <LogOut class="size-5" aria-hidden="true" /><span class="sr-only">{{ t("nav.signOut") }}</span>
        </button>
      </template>
    </Rail>
    <div :class="signedIn ? 'md:ps-[7rem]' : ''">
      <header class="mx-auto flex max-w-[1240px] items-center justify-between gap-3 px-4 pt-4 md:px-8 md:pt-6">
        <Link :to="isPerson ? '/app' : isOperator ? '/tenants' : '/signin'" class="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
          <span v-if="!signedIn" class="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-xl text-base font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
          <span :class="signedIn && 'md:sr-only'">{{ brand }}</span>
        </Link>
        <div class="flex items-center gap-2">
          <LanguageSwitcher />
          <ThemeToggle />
          <UserMenu v-if="isPerson" />
          <button
            v-if="isOperator"
            type="button"
            class="bg-card text-muted-foreground hover:text-foreground border-border/60 shadow-card inline-flex size-10 items-center justify-center rounded-xl border md:hidden"
            :aria-label="t('nav.signOut')"
            @click="operatorSignOut"
          >
            <LogOut class="size-4" />
          </button>
        </div>
      </header>
      <main :class="['mx-auto max-w-[1240px] px-4 pt-6 pb-32 md:px-8 md:pb-12', !signedIn && 'flex flex-col items-center pt-8 md:pt-14']"><VerifyBanner v-if="isPerson" /><Outlet /></main>
    </div>
  </div>
</template>
