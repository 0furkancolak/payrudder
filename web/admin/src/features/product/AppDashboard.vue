<script setup lang="ts">
import { Link, useNavigate, useParams, useSearch } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useDashboard } from "@/features/dashboard/api";
import DashboardView from "@/features/dashboard/DashboardView.vue";
import { parseQuery, toSearch, type DashQuery } from "@/features/dashboard/lib";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useApp, type Environment } from "./api";
import { defaultEnv, isEnvironment, liveState, tenantOf } from "@/shared/lib/environment";
import GoLiveChecklist from "./GoLiveChecklist.vue";
import HomeFirstRun from "./HomeFirstRun.vue";

/**
 * The dashboard of one environment of an app: the page head, and the dashboard (period, currencies, figures) which keeps its choices in the address so a
 * link shares the view. The environment switch is in the header of the frame, and the sandbox banner under it.
 *
 * The environment is the address's; without one it is where the person last was in this app (see defaultEnv), decided once the app has arrived, so the page
 * never loads one environment and then flips to the other. A live environment that is not active has no figures: the page shows its go-live checklist
 * instead of a dashboard of zeros.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const search = useSearch({ strict: false });
const navigate = useNavigate();

const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);
const env = computed<Environment | null>(() => {
  const v = (search.value as { env?: string }).env;
  if (isEnvironment(v)) return v;
  return app.data.value?.app ? defaultEnv(app.data.value.app) : null;
});
const live = computed(() => env.value === "live");
const notActive = computed(() => live.value && liveState(app.data.value?.app) !== "active");
const query = computed(() => parseQuery(search.value as Record<string, unknown>));
const source = computed(() => (appId.value && env.value && !notActive.value ? { kind: "app" as const, appId: appId.value, env: env.value } : null));
const dash = useDashboard(source, query);
const tenantId = computed(() => (env.value ? tenantOf(app.data.value?.app, env.value) : "") || (dash.data.value?.tenant_id ?? ""));

function setQuery(q: DashQuery) {
  if (!env.value) return;
  void navigate({ to: "/app/apps/$appId", params: { appId: appId.value }, search: { env: env.value, ...toSearch(q) } as never, replace: true });
}
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="flex flex-wrap items-end justify-between gap-4 px-1">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="truncate text-3xl font-semibold tracking-tight md:text-4xl">{{ app.data.value?.app?.name ?? "…" }}</h1>
          <StatusPill v-if="dash.data.value" :tone="toneOf(dash.data.value.status)">{{ t(`product.appStatus.${dash.data.value.status}`) }}</StatusPill>
          <StatusPill v-if="env" :tone="env === 'live' ? 'info' : 'neutral'">{{ t(`product.env.${env}`) }}</StatusPill>
        </div>
        <p v-if="app.data.value?.app?.slug" class="text-muted-foreground mono mt-1 text-xs">{{ app.data.value.app.slug }}</p>
      </div>
    </header>

    <HomeFirstRun :app="app.data.value?.app" />

    <Skeleton v-if="!env" class="h-64 rounded-3xl" aria-busy="true" />

    <template v-else-if="notActive">
      <GoLiveChecklist :app-id="appId" data-testid="live-checklist" />
      <p class="text-muted-foreground px-1 text-sm">
        {{ t("product.goLive.inactiveNote") }}
        <Link :to="`/app/apps/${appId}`" :search="{ env: 'sandbox' } as never" class="text-primary font-medium underline-offset-4 hover:underline" data-testid="to-sandbox">{{ t("product.goLive.toSandbox") }}</Link>
      </p>
    </template>

    <DashboardView v-else :source="source" :base="`/app/t/${tenantId}`" :query="query" @update:query="setQuery" />
  </section>
</template>
