<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Rocket, TriangleAlert } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { todayYmd } from "@/shared/lib/dates";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { useDashboard, type DashSource } from "./api";
import CurrencyTabs from "./CurrencyTabs.vue";
import DashActivity from "./DashActivity.vue";
import DashHealth from "./DashHealth.vue";
import DashKpis from "./DashKpis.vue";
import DashPayments from "./DashPayments.vue";
import DashRankings from "./DashRankings.vue";
import DashRenewals from "./DashRenewals.vue";
import DashSection from "./DashSection.vue";
import DashUsage from "./DashUsage.vue";
import { chosenCurrency, hasActivity, type DashQuery } from "./lib";
import PeriodBar from "./PeriodBar.vue";
import RevenueChart from "./RevenueChart.vue";

/**
 * The dashboard of one environment: a period, compared with the one before, in one block per currency (the tabs), with the customers, usage, renewals,
 * activity and health that are not about one currency. It asks the gateway once (`useDashboard`); a section whose service did not answer is named and the
 * rest stays. `base` is where the pages of the environment live (for the links), `source` which environment is shown. Each card is a component of its own.
 */
const props = defineProps<{ source: DashSource | null; base: string; query: DashQuery }>();
const emit = defineEmits<{ "update:query": [DashQuery] }>();
const { t } = useI18n();
const errorText = useErrorText();

const dash = useDashboard(
  () => props.source,
  () => props.query,
);
const data = computed(() => dash.data.value);
const blocks = computed(() => data.value?.currencies ?? []);
const block = computed(() => chosenCurrency(blocks.value, props.query.cur));
const cur = computed(() => block.value?.currency ?? data.value?.default_currency ?? "");
const compare = computed(() => data.value?.range?.compare ?? props.query.compare);
const today = todayYmd();
const loading = computed(() => dash.isPending.value && !data.value);
const stale = computed(() => dash.isFetching.value && !!data.value);

const unavailable = computed(() => data.value?.unavailable ?? []);
const revenueDown = computed(() => unavailable.value.includes("revenue"));
const points = computed(() =>
  (block.value?.series ?? []).map((p) => ({ date: p.date ?? "", value: p.net_minor ?? 0, ...(p.prev_net_minor !== undefined && p.prev_net_minor !== null ? { prevDate: p.prev_date ?? "", prev: p.prev_net_minor } : {}) })),
);
// A tenant that has never been charged has no currency block at all, which is as empty as a block of zeros.
const empty = computed(() => !!data.value && (!block.value || !hasActivity(block.value)) && (data.value.customers?.total ?? 0) === 0);
</script>

<template>
  <div class="flex flex-col gap-6" :aria-busy="loading || stale" data-testid="dashboard">
    <div class="flex flex-col gap-4">
      <PeriodBar :query="query" :today="today" @update:query="emit('update:query', $event)" />
      <CurrencyTabs
        v-if="blocks.length > 0"
        :currencies="blocks.map((x) => x.currency ?? '')"
        :active="cur"
        :default-currency="data?.default_currency ?? ''"
        @select="emit('update:query', { ...query, cur: $event })"
      />
    </div>

    <div v-if="dash.error.value && !data" class="bg-destructive-soft text-destructive flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3 text-sm" role="alert" data-testid="dash-error">
      <span>{{ errorText(dash.error.value) }}</span>
      <Button size="sm" variant="outline" @click="dash.refetch()">{{ t("dash.retry") }}</Button>
    </div>
    <p v-else-if="dash.error.value" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("dash.stale") }}</p>

    <p v-if="unavailable.length > 0" class="bg-warning-soft text-warning flex items-start gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="dash-degraded">
      <TriangleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      {{ t("dash.degraded", { sections: unavailable.map((s) => t(`dash.sections.${s}`)).join(", ") }) }}
    </p>

    <!-- loading: the shape of the page, not a spinner -->
    <template v-if="loading">
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4" data-testid="dash-skeleton">
        <Skeleton v-for="n in 8" :key="n" class="h-36 rounded-3xl" />
      </div>
      <div class="grid gap-5 xl:grid-cols-3"><Skeleton class="h-80 rounded-3xl xl:col-span-2" /><Skeleton class="h-80 rounded-3xl" /></div>
    </template>

    <!-- nothing yet -->
    <section v-else-if="empty" class="surface flex flex-col items-center gap-4 px-6 py-14 text-center" data-testid="dash-empty">
      <span class="bg-brand-soft text-brand-strong flex size-16 items-center justify-center rounded-3xl"><Rocket class="size-8" aria-hidden="true" /></span>
      <div>
        <h2 class="text-xl font-semibold tracking-tight">{{ t("dash.empty.title") }}</h2>
        <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">{{ t("dash.empty.body") }}</p>
      </div>
      <div class="flex flex-wrap justify-center gap-2">
        <Button as-child><Link :to="`${base}/customers`">{{ t("shell.items.customers") }}</Link></Button>
        <Button as-child variant="outline"><Link :to="`${base}/packages`">{{ t("shell.items.packages") }}</Link></Button>
      </div>
    </section>

    <template v-else-if="data && block">
      <DashKpis :data="data" :block="block" :cur="cur" :compare="compare" :base="base" />

      <!-- revenue and payments -->
      <div class="grid gap-5 xl:grid-cols-3">
        <DashSection :title="t('dash.chart.title')" class="xl:col-span-2" :down="revenueDown" tall>
          <template #head>
            <div class="flex flex-wrap items-baseline justify-between gap-2">
              <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.chart.title") }}</h2>
              <p class="text-muted-foreground text-xs">{{ t("dash.chart.note") }}</p>
            </div>
          </template>
          <RevenueChart :points="points" :currency="cur" :granularity="data.range?.granularity ?? 'day'" :compare="compare" />
        </DashSection>
        <DashPayments :data="data" :block="block" />
      </div>

      <DashRankings :data="data" :block="block" :cur="cur" />

      <!-- usage, renewals, activity -->
      <div class="grid gap-5 lg:grid-cols-2 xl:grid-cols-3">
        <DashUsage :data="data" :base="base" />
        <DashRenewals :data="data" :block="block" :cur="cur" />
        <DashActivity :data="data" />
      </div>

      <DashHealth :data="data" :base="base" />
    </template>
  </div>
</template>
