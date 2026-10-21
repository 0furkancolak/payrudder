<script setup lang="ts">
import { useNavigate, useParams, useSearch } from "@tanstack/vue-router";
import { FlaskConical } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useDashboard } from "@/features/dashboard/api";
import DashboardView from "@/features/dashboard/DashboardView.vue";
import { parseQuery, toSearch, type DashQuery } from "@/features/dashboard/lib";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useApp, type Environment } from "./api";
import { isEnvironment, tenantOf } from "./lib";

/**
 * The dashboard of one environment of an app: the page head, and the dashboard (period, currencies, figures) which keeps its choices in the address so a
 * link shares the view. The environment switch is in the header of the frame.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const search = useSearch({ strict: false });
const navigate = useNavigate();

const appId = computed(() => String(params.value.appId ?? ""));
const env = computed<Environment>(() => {
  const v = (search.value as { env?: string }).env;
  return isEnvironment(v) ? v : "live";
});
const query = computed(() => parseQuery(search.value as Record<string, unknown>));
const app = useApp(appId);
const source = computed(() => (appId.value ? { kind: "app" as const, appId: appId.value, env: env.value } : null));
const dash = useDashboard(source, query);
const tenantId = computed(() => tenantOf(app.data.value?.app, env.value) || (dash.data.value?.tenant_id ?? ""));

function setQuery(q: DashQuery) {
  void navigate({ to: "/app/apps/$appId", params: { appId: appId.value }, search: { ...(env.value === "sandbox" ? { env: "sandbox" } : {}), ...toSearch(q) } as never, replace: true });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="flex flex-wrap items-end justify-between gap-4 px-1">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="truncate text-3xl font-semibold tracking-tight md:text-4xl">{{ app.data.value?.app?.name ?? "…" }}</h1>
          <StatusPill v-if="dash.data.value" :tone="toneOf(dash.data.value.status)">{{ t(`product.appStatus.${dash.data.value.status}`) }}</StatusPill>
          <StatusPill :tone="env === 'live' ? 'info' : 'neutral'">{{ t(`product.env.${env}`) }}</StatusPill>
        </div>
        <p v-if="app.data.value?.app?.slug" class="text-muted-foreground mono mt-1 text-xs">{{ app.data.value.app.slug }}</p>
      </div>
    </header>

    <p v-if="env === 'sandbox'" class="bg-brand-soft text-brand-strong flex items-center gap-2 rounded-2xl px-4 py-3 text-sm" role="note"><FlaskConical class="size-4 shrink-0" aria-hidden="true" />{{ t("product.env.sandboxNote") }}</p>

    <DashboardView :source="source" :base="`/app/t/${tenantId}`" :query="query" @update:query="setQuery" />
  </section>
</template>
