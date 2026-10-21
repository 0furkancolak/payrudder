<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronsLeft, ChevronsRight } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { setSidebarCollapsed, sidebarCollapsed } from "@/shared/lib/prefs";
import SidebarNav from "./SidebarNav.vue";
import type { NavItem, NavSection } from "./nav";

/**
 * The sidebar of a wide screen: open by default (icons and labels), foldable to an icon rail with the choice remembered. It is a floating white card on
 * the grey page, like the cards beside it. The same navigation fills the phone drawer (MobileDrawer).
 */
defineProps<{ sections: NavSection[]; current: NavItem | undefined; brand: string; home: string }>();
const { t } = useI18n();
</script>

<template>
  <aside
    data-testid="sidebar"
    :data-collapsed="sidebarCollapsed"
    :class="[
      'bg-card shadow-card fixed inset-y-4 start-4 z-30 hidden flex-col rounded-3xl py-4 transition-[width] duration-200 lg:flex',
      sidebarCollapsed ? 'w-[4.5rem] px-2.5' : 'w-64 px-3',
    ]"
  >
    <Link :to="home" :class="['mb-5 flex items-center gap-2.5 rounded-2xl', sidebarCollapsed ? 'justify-center' : 'px-2']" :title="brand">
      <span class="bg-brand text-rail flex size-10 shrink-0 items-center justify-center rounded-2xl text-lg font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
      <span :class="sidebarCollapsed ? 'sr-only' : 'truncate text-lg font-semibold tracking-tight'">{{ brand }}</span>
    </Link>

    <div class="-mx-1 min-h-0 flex-1 overflow-y-auto px-1">
      <SidebarNav :sections="sections" :current="current" :collapsed="sidebarCollapsed" :label="t('shell.navLabel')" />
    </div>

    <div class="mt-3 flex flex-col gap-0.5 border-t pt-3">
      <button
        type="button"
        data-testid="sidebar-toggle"
        :aria-expanded="!sidebarCollapsed"
        :aria-label="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :title="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :class="['text-muted-foreground hover:bg-muted hover:text-foreground flex min-h-10 items-center gap-3 rounded-2xl text-sm font-medium', sidebarCollapsed ? 'justify-center' : 'px-3.5']"
        @click="setSidebarCollapsed(!sidebarCollapsed)"
      >
        <ChevronsRight v-if="sidebarCollapsed" class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <ChevronsLeft v-else class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <span v-if="!sidebarCollapsed">{{ t("shell.collapse") }}</span>
      </button>
    </div>
  </aside>
</template>
