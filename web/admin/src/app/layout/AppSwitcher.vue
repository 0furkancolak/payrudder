<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { Check, ChevronsUpDown, LayoutGrid, Plus } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { App } from "@/features/product/api";
import type { Environment } from "@/shared/lib/environment";
import { ALL_APPS } from "@/shared/lib/scope";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";
import { scopeTarget } from "./scopeNav";

/**
 * The app switcher at the top of the sidebar. An app is always selected; the first entry is "All apps", which makes every list, dashboard and report
 * show the data of every app. The choice is written into the address (`?app=<id|all>`); choosing keeps the person on the same page of the other app.
 */
const props = defineProps<{ apps: App[]; scope: string | null; path: string; env: Environment; collapsed?: boolean }>();
const emit = defineEmits<{ navigate: [] }>();
const { t } = useI18n();
const navigate = useNavigate();

const all = computed(() => props.scope === ALL_APPS);
const current = computed(() => props.apps.find((a) => a.id === props.scope));
const name = computed(() => (all.value ? t("shell.allApps") : (current.value?.name ?? "…")));

function pick(id: string) {
  const target = scopeTarget(props.path, id, props.apps, props.env);
  emit("navigate");
  void navigate({ to: target.to, search: target.search as never });
}

function create() {
  emit("navigate");
  void navigate({ to: "/app", search: { app: ALL_APPS, sheet: "new-app" } as never });
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        data-testid="app-switcher"
        :data-scope="scope ?? ''"
        :class="[
          'bg-rail-accent text-rail-accent-foreground hover:bg-rail-accent/80 flex min-h-11 w-full min-w-0 items-center gap-2.5 rounded-2xl text-sm font-medium transition-colors',
          collapsed ? 'justify-center px-0' : 'px-3',
        ]"
        :aria-label="t('shell.switchApp')"
        :title="collapsed ? name : undefined"
      >
        <span class="bg-rail-primary/20 text-rail-primary flex size-6 shrink-0 items-center justify-center rounded-lg text-xs font-bold" aria-hidden="true">
          <LayoutGrid v-if="all" class="size-3.5" />
          <template v-else>{{ (current?.name ?? "?").slice(0, 1).toUpperCase() }}</template>
        </span>
        <template v-if="!collapsed">
          <span class="min-w-0 flex-1 truncate text-start" data-testid="app-switcher-name">{{ name }}</span>
          <ChevronsUpDown class="text-rail-muted size-4 shrink-0" aria-hidden="true" />
        </template>
        <span v-else class="sr-only">{{ name }}</span>
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="min-w-60">
      <DropdownMenuLabel class="text-muted-foreground text-xs">{{ t("shell.apps") }}</DropdownMenuLabel>
      <DropdownMenuItem class="rounded-xl" data-testid="switch-app-all" @select="pick(ALL_APPS)">
        <LayoutGrid class="size-4" aria-hidden="true" />
        <span class="flex-1 truncate">{{ t("shell.allApps") }}</span>
        <Check v-if="all" class="size-4" aria-hidden="true" />
      </DropdownMenuItem>
      <DropdownMenuItem v-for="a in apps" :key="a.id" class="rounded-xl" :data-testid="`switch-app-${a.slug}`" @select="pick(a.id ?? '')">
        <span class="bg-brand-soft text-brand-strong flex size-5 shrink-0 items-center justify-center rounded-md text-[0.65rem] font-bold" aria-hidden="true">{{ (a.name ?? "?").slice(0, 1).toUpperCase() }}</span>
        <span class="flex-1 truncate">{{ a.name }}</span>
        <Check v-if="a.id === scope" class="size-4" aria-hidden="true" />
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem class="rounded-xl" data-testid="switch-app-new" @select="create"><Plus class="size-4" aria-hidden="true" />{{ t("shell.newApp") }}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
