<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { Check, ChevronsUpDown, Plus } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { App, Environment } from "@/features/product/api";
import { tenantOf } from "@/features/product/lib";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";
import { SegmentedControl } from "@/shared/ui/segmented";
import type { ShellContext } from "./nav";

/**
 * The app switcher and the environment switcher of the header, for a person inside an app. The app switcher lists the organisation's apps; the
 * environment switcher moves the page between the app's live and sandbox tenants and keeps the person on the same section.
 */
const props = defineProps<{ context: ShellContext; apps: App[]; appName: string; path: string }>();
const { t } = useI18n();
const navigate = useNavigate();

const env = computed<Environment>(() => props.context.env ?? "live");
const current = computed(() => props.apps.find((a) => a.id === props.context.appId));
const hasLive = computed(() => (current.value ? tenantOf(current.value, "live") !== "" : true));
const options = computed(() => [...(hasLive.value ? [{ value: "live", label: t("product.env.live") }] : []), { value: "sandbox", label: t("product.env.sandbox") }]);

function openApp(app: App) {
  const e: Environment = tenantOf(app, env.value) ? env.value : tenantOf(app, "live") ? "live" : "sandbox";
  void navigate({ to: "/app/apps/$appId", params: { appId: app.id ?? "" }, search: { env: e } as never });
}

function pickEnv(value: string) {
  if (value !== "live" && value !== "sandbox") return;
  const app = current.value;
  if (!app) return;
  const target = tenantOf(app, value);
  const tid = props.context.tenantId;
  // On a tenant page the same section of the other tenant; on the dashboard the same page with the other environment.
  if (props.path.startsWith("/app/t/") && tid && target) {
    void navigate({ to: props.path.replace(`/app/t/${tid}`, `/app/t/${target}`) as never });
  } else if (props.path.startsWith("/app/apps/")) {
    void navigate({ to: props.path as never, search: { env: value } as never, replace: true });
  } else {
    void navigate({ to: "/app/apps/$appId", params: { appId: app.id ?? "" }, search: { env: value } as never });
  }
}
</script>

<template>
  <div class="flex min-w-0 flex-wrap items-center gap-2" data-testid="context-switcher">
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <button
          type="button"
          class="bg-card border-border/60 shadow-card hover:bg-accent inline-flex h-10 max-w-56 min-w-0 items-center gap-2 rounded-xl border px-3 text-sm font-medium"
          :aria-label="t('shell.switchApp')"
        >
          <span class="bg-brand-soft text-brand-strong flex size-6 shrink-0 items-center justify-center rounded-lg text-xs font-bold" aria-hidden="true">{{ (appName || "?").slice(0, 1).toUpperCase() }}</span>
          <span class="truncate">{{ appName || "…" }}</span>
          <ChevronsUpDown class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="min-w-60">
        <DropdownMenuLabel class="text-muted-foreground text-xs">{{ t("shell.apps") }}</DropdownMenuLabel>
        <DropdownMenuItem v-for="a in apps" :key="a.id" class="rounded-xl" :data-testid="`switch-app-${a.slug}`" @select="openApp(a)">
          <span class="flex-1 truncate">{{ a.name }}</span>
          <Check v-if="a.id === context.appId" class="size-4" aria-hidden="true" />
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem class="rounded-xl" @select="navigate({ to: '/app' })"><Plus class="size-4" />{{ t("shell.allApps") }}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <SegmentedControl :model-value="env" :options="options" :label="t('product.env.label')" data-testid="env-switch" @update:model-value="pickEnv" />
  </div>
</template>
