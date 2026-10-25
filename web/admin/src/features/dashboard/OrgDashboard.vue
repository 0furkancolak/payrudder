<script setup lang="ts">
import { useQueries } from "@tanstack/vue-query";
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { POLL } from "@/shared/api/limits";
import { todayYmd } from "@/shared/lib/dates";
import { useFormat } from "@/shared/lib/format";
import { type App } from "@/features/product/api";
import { tenantOf } from "@/shared/lib/environment";
import { Skeleton } from "@/shared/ui/skeleton";
import { SegmentedControl } from "@/shared/ui/segmented";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { dashKeys, fetchDashboard } from "./api";
import BarList from "./BarList.vue";
import CurrencyTabs from "./CurrencyTabs.vue";
import KpiCard from "./KpiCard.vue";
import { bpPercent, delta, mergedCurrencies, mergedSeries, ratioBp, sumBlocks, type DashCurrency, type DashQuery, type Dashboard } from "./lib";
import PeriodBar from "./PeriodBar.vue";
import RevenueChart from "./RevenueChart.vue";
import type { Environment } from "@/shared/lib/environment";

/**
 * The organisation's dashboard: every app over the same period, put together per currency. Several apps selling in the same currency are added up (that is the
 * same unit); different currencies never are, hence the tabs. Each app is read with the dashboard endpoint, at most twelve of them, so the page is as accurate
 * as the app dashboards and agrees with them. An app that does not answer is named and left out.
 */
const props = defineProps<{ apps: App[]; query: DashQuery; env: Environment }>();
const emit = defineEmits<{ "update:query": [DashQuery]; "update:env": [Environment] }>();
const { t } = useI18n();
const fmt = useFormat();

/** At most this many apps are read, each with its own dashboard request. */
const MAX_APPS = 12;
/** Each app's dashboard is read again this often while the page is open, and counts as fresh for this long. */
const REFRESH_MS = POLL.figures;
const FRESH_MS = 15_000;
const shown = computed(() => props.apps.filter((a) => tenantOf(a, props.env) !== "").slice(0, MAX_APPS));
const results = useQueries({
  queries: computed(() =>
    shown.value.map((a) => {
      const source = { kind: "app" as const, appId: a.id ?? "", env: props.env };
      return { queryKey: dashKeys.of(source, props.query), queryFn: () => fetchDashboard(source, props.query), refetchInterval: REFRESH_MS, staleTime: FRESH_MS };
    }),
  ),
});

interface Row {
  app: App;
  data: Dashboard | undefined;
  failed: boolean;
}
const rows = computed<Row[]>(() => shown.value.map((app, i) => ({ app, data: results.value[i]?.data as Dashboard | undefined, failed: !!results.value[i]?.isError && !results.value[i]?.data })));
const loading = computed(() => shown.value.length > 0 && results.value.some((r) => r.isPending && !r.data));
const down = computed(() => rows.value.filter((r) => r.failed || (r.data?.unavailable ?? []).includes("revenue")));
const compare = computed(() => props.query.compare);
const today = todayYmd();

const currencies = computed(() => mergedCurrencies(rows.value.map((r) => r.data)));
const cur = computed(() => (props.query.cur && currencies.value.includes(props.query.cur) ? props.query.cur : (currencies.value[0] ?? "")));

/** Every app's dashboard with its block of the chosen currency. */
const blocks = computed(() => rows.value.map((r) => ({ app: r.app, data: r.data, block: r.data?.currencies?.find((c) => c.currency === cur.value) as DashCurrency | undefined })));
const sum = (pick: (b: DashCurrency) => number | null | undefined) => sumBlocks(blocks.value.map((x) => x.block), pick);
const net = computed(() => sum((b) => b.revenue?.net_minor));
const prevNet = computed(() => sum((b) => b.prev_revenue?.net_minor));
const mrr = computed(() => sum((b) => b.subscriptions?.mrr_minor));
const prevMrr = computed(() => sum((b) => b.prev_subscriptions?.mrr_minor));
const failed = computed(() => sum((b) => b.payments?.failed));
const paid = computed(() => sum((b) => b.payments?.paid));
const prevFailed = computed(() => sum((b) => b.prev_payments?.failed));
const prevPaid = computed(() => sum((b) => b.prev_payments?.paid));
const rate = computed(() => ratioBp(failed.value, failed.value + paid.value));
const prevRate = computed(() => ratioBp(prevFailed.value, prevFailed.value + prevPaid.value));
const customerSum = (pick: (c: NonNullable<Dashboard["customers"]>) => number | undefined) => rows.value.reduce((n, r) => n + (r.data?.customers ? (pick(r.data.customers) ?? 0) : 0), 0);
const newCustomers = computed(() => customerSum((c) => c.new));
const prevNewCustomers = computed(() => customerSum((c) => c.prev_new));
const totalCustomers = computed(() => customerSum((c) => c.total));

/** The series of the apps, added point by point (same period, same buckets). */
const points = computed(() => mergedSeries(blocks.value.map((x) => x.block)));
const granularity = computed(() => rows.value.find((r) => r.data)?.data?.range?.granularity ?? "day");
const byApp = computed(() =>
  blocks.value
    .map((x) => ({ key: x.app.id ?? "", label: x.app.name ?? "—", sub: x.block?.subscriptions ? t("dash.top.perMonth", { amount: money(x.block.subscriptions.mrr_minor) }) : "", value: x.block?.revenue?.net_minor ?? 0, text: money(x.block?.revenue?.net_minor) }))
    .filter((x) => x.value > 0)
    .sort((a, b) => b.value - a.value),
);
const activeSubscriptions = computed(() => sum((b) => b.subscriptions?.active));

const money = (minor: number | null | undefined) => fmt.amount(minor, cur.value);
const count = fmt.count;
const pct = fmt.percent;
const dNet = computed(() => (compare.value ? delta(net.value, prevNet.value) : null));
const dMrr = computed(() => (compare.value ? delta(mrr.value, prevMrr.value) : null));
const dRate = computed(() => (compare.value && rate.value !== null && prevRate.value !== null ? delta(bpPercent(rate.value), bpPercent(prevRate.value)) : null));
const dCustomers = computed(() => (compare.value ? delta(newCustomers.value, prevNewCustomers.value) : null));
const envOptions = computed(() => [
  { value: "live", label: t("product.env.live") },
  { value: "sandbox", label: t("product.env.sandbox") },
]);
</script>

<template>
  <section class="flex flex-col gap-6" data-testid="org-dashboard" :aria-label="t('dash.org.title')">
    <div class="flex flex-wrap items-end justify-between gap-3 px-1">
      <div>
        <h2 class="text-xl font-semibold tracking-tight">{{ t("dash.org.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("dash.org.intro") }}</p>
      </div>
      <SegmentedControl :model-value="env" :options="envOptions" :label="t('product.env.label')" @update:model-value="emit('update:env', $event === 'sandbox' ? 'sandbox' : 'live')" />
    </div>

    <div class="flex flex-col gap-4">
      <PeriodBar :query="query" :today="today" @update:query="emit('update:query', $event)" />
      <CurrencyTabs v-if="currencies.length > 0" :currencies="currencies" :active="cur" default-currency="" @select="emit('update:query', { ...query, cur: $event })" />
    </div>

    <p v-if="down.length > 0" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status" data-testid="org-degraded">
      {{ t("dash.degraded", { sections: down.map((r) => r.app.name).join(", ") }) }}
    </p>

    <div v-if="loading" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4" aria-busy="true"><Skeleton v-for="n in 4" :key="n" class="h-36 rounded-3xl" /></div>

    <template v-else-if="currencies.length > 0">
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard testid="org-revenue" :label="t('dash.kpi.net')" :delta="dNet" :previous="compare ? money(prevNet) : undefined" :spark="points.map((p) => p.value)">
          <template #value>{{ money(net) }}</template>
          <template #sub>{{ t("dash.org.apps", { n: shown.length }, shown.length) }}</template>
        </KpiCard>
        <KpiCard testid="org-mrr" :label="t('dash.kpi.mrr')" :delta="dMrr" :previous="compare ? money(prevMrr) : undefined">
          <template #value>{{ money(mrr) }}</template>
          <template #sub>{{ t("dash.kpi.mrrSub", { arr: money(mrr * 12), n: count(activeSubscriptions) }) }}</template>
        </KpiCard>
        <KpiCard testid="org-failed" good="down" :label="t('dash.kpi.failedRate')" :delta="dRate" :previous="prevRate !== null ? pct(bpPercent(prevRate)) : undefined">
          <template #value>{{ pct(bpPercent(rate)) }}</template>
          <template #sub>{{ t("dash.kpi.failedSub", { failed: count(failed), attempts: count(failed + paid) }) }}</template>
        </KpiCard>
        <KpiCard testid="org-customers" :label="t('dash.kpi.customers')" :delta="dCustomers" :previous="compare ? count(prevNewCustomers) : undefined">
          <template #value>{{ count(newCustomers) }}</template>
          <template #sub>{{ t("dash.kpi.customersSub", { total: count(totalCustomers) }) }}</template>
        </KpiCard>
      </div>

      <div class="grid gap-5 xl:grid-cols-3">
        <section class="surface flex flex-col gap-4 p-6 xl:col-span-2" :aria-label="t('dash.chart.title')">
          <h3 class="text-lg font-semibold tracking-tight">{{ t("dash.chart.title") }}</h3>
          <RevenueChart :points="points" :currency="cur" :granularity="granularity" :compare="compare" />
        </section>
        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.org.byApp')" data-testid="org-by-app">
          <h3 class="text-lg font-semibold tracking-tight">{{ t("dash.org.byApp") }}</h3>
          <p v-if="byApp.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.org.none") }}</p>
          <BarList v-else :items="byApp" />
          <ul class="divide-border/70 mt-1 divide-y border-t pt-1">
            <li v-for="x in blocks" :key="x.app.id" class="flex items-center gap-3 py-2.5 text-sm">
              <span class="min-w-0 flex-1 truncate font-medium">{{ x.app.name }}</span>
              <StatusPill v-if="x.data" :tone="toneOf(x.data.status)">{{ t(`product.appStatus.${x.data.status}`) }}</StatusPill>
              <Link :to="`/app/apps/${x.app.id}`" :search="(env === 'sandbox' ? { env: 'sandbox' } : {}) as never" class="text-primary text-xs font-medium hover:underline">{{ t("dash.org.open") }}</Link>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </section>
</template>
