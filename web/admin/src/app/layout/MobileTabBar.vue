<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Menu, Search } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";
import type { NavItem, NavSection } from "./nav";

/**
 * The tab bar of a phone or a tablet (below the width where the sidebar shows): the pages used most (dashboard, customers, payments) when the place has
 * them, the search (the command palette) and "More", which opens the drawer with the whole navigation.
 */
const props = defineProps<{ sections: NavSection[]; current: NavItem | undefined; drawer: boolean }>();
const emit = defineEmits<{ search: []; more: [] }>();
const { t } = useI18n();

const TABS = ["dashboard", "tenants", "customers", "payments"];
const tabs = computed(() =>
  TABS.map((key) => props.sections.flatMap((s) => s.items).find((it) => it.key === key))
    .filter((it): it is NavItem => !!it)
    .slice(0, 3),
);
const isCurrent = (it: NavItem) => props.current?.key === it.key && props.current?.to === it.to;
const tab = (on = false) =>
  cn("flex min-h-14 min-w-0 flex-1 flex-col items-center justify-center gap-1 text-[0.7rem] font-medium transition-colors", on ? "text-primary" : "text-muted-foreground hover:text-foreground");
</script>

<template>
  <nav :aria-label="t('shell.tabsLabel')" data-testid="tab-bar" class="bg-background/95 fixed inset-x-0 bottom-0 z-30 flex border-t px-1 pb-[env(safe-area-inset-bottom)] backdrop-blur lg:hidden">
    <Link v-for="it in tabs" :key="it.key" :to="it.to" :search="(it.search ?? {}) as never" :class="tab(isCurrent(it))" :aria-current="isCurrent(it) ? 'page' : undefined" :data-tab="it.key">
      <component :is="it.icon" class="size-5" aria-hidden="true" />
      <span class="max-w-full truncate px-1">{{ t(`shell.items.${it.label}`) }}</span>
    </Link>
    <button type="button" :class="tab()" data-testid="tab-search" aria-haspopup="dialog" @click="emit('search')">
      <Search class="size-5" aria-hidden="true" />
      <span>{{ t("shell.search") }}</span>
    </button>
    <button type="button" :class="tab(drawer)" data-testid="menu-button" aria-haspopup="dialog" :aria-expanded="drawer" :aria-label="t('shell.openMenu')" @click="emit('more')">
      <Menu class="size-5" aria-hidden="true" />
      <span aria-hidden="true">{{ t("shell.more") }}</span>
    </button>
  </nav>
</template>
