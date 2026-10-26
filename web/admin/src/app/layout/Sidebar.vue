<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronsLeft, ChevronsRight } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { setSidebarCollapsed, sidebarCollapsed } from "@/shared/lib/prefs";
import { BrandLogo } from "@/shared/ui/brand-logo";
import SidebarNav, { type SidebarApp } from "./SidebarNav.vue";
import type { Environment } from "@/shared/lib/environment";
import type { NavItem, NavSection } from "./nav";

/**
 * The sidebar of a wide screen: open by default (icons and labels), foldable to an icon rail with the choice remembered. It is a floating card on
 * the grey page that stays dark in both themes (DESIGN.md). The same navigation fills the phone drawer (MobileDrawer).
 */
defineProps<{ sections: NavSection[]; current: NavItem | undefined; brand: string; home: string; apps?: SidebarApp[]; appId?: string; env?: Environment; nest?: boolean }>();
const { t } = useI18n();
</script>

<template>
  <aside
    data-testid="sidebar"
    :data-collapsed="sidebarCollapsed"
    :class="[
      'bg-rail text-rail-foreground shadow-card fixed inset-y-4 start-4 z-30 hidden flex-col rounded-3xl py-4 transition-[width] duration-200 lg:flex',
      sidebarCollapsed ? 'w-[4.5rem] px-2.5' : 'w-64 px-3',
    ]"
  >
    <Link :to="home" :class="['mb-5 flex items-center gap-2.5 rounded-2xl', sidebarCollapsed ? 'justify-center' : 'px-2']" :title="brand">
      <BrandLogo :variant="sidebarCollapsed ? 'icon' : 'wordmark'" on="rail" />
    </Link>

    <div class="-mx-1 min-h-0 flex-1 overflow-y-auto px-1">
      <SidebarNav :sections="sections" :current="current" :collapsed="sidebarCollapsed" :apps="apps" :app-id="appId" :env="env" :nest="nest" :label="t('shell.navLabel')" />
    </div>

    <div class="border-rail-border mt-3 flex flex-col gap-0.5 border-t pt-3">
      <button
        type="button"
        data-testid="sidebar-toggle"
        :aria-expanded="!sidebarCollapsed"
        :aria-label="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :title="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :class="['text-rail-foreground hover:bg-rail-accent hover:text-rail-accent-foreground flex min-h-10 items-center gap-3 rounded-2xl text-sm font-medium', sidebarCollapsed ? 'justify-center' : 'px-3.5']"
        @click="setSidebarCollapsed(!sidebarCollapsed)"
      >
        <ChevronsRight v-if="sidebarCollapsed" class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <ChevronsLeft v-else class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <span v-if="!sidebarCollapsed">{{ t("shell.collapse") }}</span>
      </button>
    </div>
  </aside>
</template>
