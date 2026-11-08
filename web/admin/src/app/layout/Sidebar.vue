<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronsLeft, ChevronsRight, Search } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { setSidebarCollapsed, sidebarCollapsed } from "@/shared/lib/prefs";
import { BrandLogo } from "@/shared/ui/brand-logo";
import type { App } from "@/features/product/api";
import type { Environment } from "@/shared/lib/environment";
import AppSwitcher from "./AppSwitcher.vue";
import SidebarNav from "./SidebarNav.vue";
import type { NavItem, NavSection } from "./nav";

/**
 * The sidebar of a wide screen: open by default (icons and labels), foldable to an icon rail with the choice remembered. It sits flush with the edge,
 * a shade off the page, with the app switcher and the search (the command palette, ⌘K) at its top. The same navigation fills the phone drawer (MobileDrawer).
 */
defineProps<{ sections: NavSection[]; current: NavItem | undefined; brand: string; home: string; apps?: App[]; scope?: string | null; env?: Environment; path: string }>();
const emit = defineEmits<{ search: [] }>();
const { t } = useI18n();
const mac = /Mac|iPhone|iPad/.test(navigator.platform);
</script>

<template>
  <aside
    data-testid="sidebar"
    :data-collapsed="sidebarCollapsed"
    :class="[
      'bg-rail text-rail-foreground border-rail-border fixed inset-y-0 start-0 z-30 hidden flex-col border-e py-4 transition-[width] duration-200 lg:flex',
      sidebarCollapsed ? 'w-[4.5rem] px-2.5' : 'w-64 px-3',
    ]"
  >
    <Link :to="home" :class="['mb-4 flex items-center gap-2.5 rounded-lg', sidebarCollapsed ? 'justify-center' : 'px-2']" :title="brand">
      <BrandLogo :variant="sidebarCollapsed ? 'icon' : 'wordmark'" />
    </Link>

    <div v-if="apps && env" class="mb-2"><AppSwitcher :apps="apps" :scope="scope ?? null" :path="path" :env="env" :collapsed="sidebarCollapsed" /></div>
    <button
      type="button"
      data-testid="sidebar-search"
      aria-haspopup="dialog"
      :aria-label="t('shell.search')"
      :title="`${t('shell.search')} (${mac ? '⌘' : 'Ctrl+'}K)`"
      :class="['text-rail-muted hover:text-rail-accent-foreground border-rail-border bg-background mb-4 flex min-h-9 items-center gap-2.5 rounded-lg border text-sm transition-colors', sidebarCollapsed ? 'justify-center' : 'px-3']"
      @click="emit('search')"
    >
      <Search class="size-4 shrink-0" aria-hidden="true" />
      <template v-if="!sidebarCollapsed">
        <span class="flex-1 text-start">{{ t("shell.search") }}</span>
        <kbd class="bg-rail-accent rounded px-1.5 font-sans text-[0.7rem]">{{ mac ? "⌘K" : "Ctrl K" }}</kbd>
      </template>
    </button>

    <!-- overscroll-behavior: contain keeps a scroll that reaches the end of the sidebar from carrying on into the page behind it. -->
    <div class="-mx-1 min-h-0 flex-1 overflow-y-auto overscroll-contain px-1" data-testid="sidebar-scroll">
      <SidebarNav :sections="sections" :current="current" :collapsed="sidebarCollapsed" :label="t('shell.navLabel')" />
    </div>

    <div class="border-rail-border mt-3 flex flex-col gap-0.5 border-t pt-3">
      <button
        type="button"
        data-testid="sidebar-toggle"
        :aria-expanded="!sidebarCollapsed"
        :aria-label="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :title="sidebarCollapsed ? t('shell.expand') : t('shell.collapse')"
        :class="['text-rail-foreground hover:bg-rail-accent hover:text-rail-accent-foreground flex min-h-9 items-center gap-3 rounded-lg text-sm font-medium', sidebarCollapsed ? 'justify-center' : 'px-3']"
        @click="setSidebarCollapsed(!sidebarCollapsed)"
      >
        <ChevronsRight v-if="sidebarCollapsed" class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <ChevronsLeft v-else class="size-[1.15rem] shrink-0 rtl:rotate-180" aria-hidden="true" />
        <span v-if="!sidebarCollapsed">{{ t("shell.collapse") }}</span>
      </button>
    </div>
  </aside>
</template>
