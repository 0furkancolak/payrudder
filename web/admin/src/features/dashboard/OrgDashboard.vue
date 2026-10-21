<script setup lang="ts">
import { useQueries } from "@tanstack/vue-query";
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { type App } from "@/features/product/api";
import { tenantOf } from "@/features/product/lib";
import { formatMinor } from "@/shared/lib/money";
import { Skeleton } from "@/shared/ui/skeleton";
import { SegmentedControl } from "@/shared/ui/segmented";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { dashKeys, fetchDashboard } from "./api";
import BarList from "./BarList.vue";
import CurrencyTabs from "./CurrencyTabs.vue";
import KpiCard from "./KpiCard.vue";
import { bpPercent, delta, type DashCurrency, type DashQuery, type Dashboard } from "./lib";
import PeriodBar from "./PeriodBar.vue";
import RevenueChart from "./RevenueChart.vue";

/**
 * The organisation's dashboard: every app over the same period, put together per currency. Several apps selling in the same currency are added up (that is the
 * same unit); different currencies never are, hence the tabs. Each app is read with the dashboard endpoint, at most twelve of them, so the page is as accurate
 * as the app dashboards and agrees with them. An app that does not answer is named and left out.
 */
const props = defineProps<{ apps: App[]; query: DashQuery; env: "live" | "sandbox" }>();
const emit = defineEmits<{ "update:query": [DashQuery]; "update:env": ["live" | "sandbox"] }>();
const { t, locale } = useI18n();

const MAX_APPS = 12;
const shown = computed(() => props.apps.filter((a) => tenantOf(a, props.env) !== "").slice(0, MAX_APPS));
const results = useQueries({
  queries: computed(() =>
    shown.value.map((a) => {
      const source = { kind: "app" as const, appId: a.id ?? "", env: props.env };
      return { queryKey: dashKeys.of(source, props.query), queryFn: () => fetchDashboard(source, props.query), refetchInterval: 60_000, staleTime: 15_000 };
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
const today = new Date().toLocaleDateString("en-CA");

const currencies = computed(() => {
  const by = new Map<string, number>();
  for (const r of rows.value) for (const c of r.data?.currencies ?? []) by.set(c.currency ?? "", (by.get(c.currency ?? "") ?? 0) + (c.revenue?.net_minor ?? 0));
  return [...by.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([c]) => c);
});
const cur = computed(() => (props.query.cur && currencies.value.includes(props.query.cur) ? props.query.cur : (currencies.value[0] ?? "")));

/** Every app's block of the chosen currency. */
const blocks = computed(() => rows.value.map((r) => ({ app: r.app, block: r.data?.currencies?.find((c) => c.currency === cur.value) as DashCurrency | undefined })));
const sum = (pick: (b: DashCurrency) => number | null | undefined) => blocks.value.reduce((n, x) => n + (x.block ? (pick(x.block) ?? 0) : 0), 0);
const net = computed(() => sum((b) => b.revenue?.net_minor));
const prevNet = computed(() => sum((b) => b.prev_revenue?.net_minor));
const mrr = computed(() => sum((b) => b.subscriptions?.mrr_minor));
const prevMrr = computed(() => sum((b) => b.prev_subscriptions?.mrr_minor));
const failed = computed(() => sum((b) => b.payments?.failed));
const paid = computed(() => sum((b) => b.payments?.paid));
const prevFailed = computed(() => sum((b) => b.prev_payments?.failed));
const prevPaid = computed(() => sum((b) => b.prev_payments?.paid));
const rate = computed(() => (failed.value + paid.value > 0 ? Math.round((failed.value * 10000) / (failed.value + paid.value)) : null));
const prevRate = computed(() => (prevFailed.value + prevPaid.value > 0 ? Math.round((prevFailed.value * 10000) / (prevFailed.value + prevPaid.value)) : null));
const newCustomers = computed(() => rows.value.reduce((n, r) => n + (r.data?.customers?.new ?? 0), 0));
const prevNewCustomers = computed(() => rows.value.reduce((n, r) => n + (r.data?.customers?.prev_new ?? 0), 0));
const totalCustomers = computed(() => rows.value.reduce((n, r) => n + (r.data?.customers?.total ?? 0), 0));

/** The series of the apps, added point by point (same period, same buckets). */
const points = computed(() => {
  const length = Math.max(0, ...blocks.value.map((x) => x.block?.series?.length ?? 0));
  return Array.from({ length }, (_, i) => {
    let value = 0;
    let prev = 0;
    let hasPrev = false;
    let date = "";
    let prevDate = "";
    for (const x of blocks.value) {
      const p = x.block?.series?.[i];
      if (!p) continue;
      date = p.date ?? date;
      value += p.net_minor ?? 0;
      if (p.prev_net_minor !== undefined && p.prev_net_minor !== null) {
        hasPrev = true;
        prev += p.prev_net_minor;
        prevDate = p.prev_date ?? prevDate;
      }
    }
    return { date, value, ...(hasPrev ? { prev, prevDate } : {}) };
  });
});
const granularity = computed(() => rows.value.find((r) => r.data)?.data?.range?.granularity ?? "day");
const byApp = computed(() =>
  blocks.value
    .map((x) => ({ key: x.app.id ?? "", label: x.app.name ?? "—", sub: x.block?.subscriptions ? t("dash.top.perMonth", { amount: money(x.block.subscriptions.mrr_minor) }) : "", value: x.block?.revenue?.net_minor ?? 0, text: money(x.block?.revenue?.net_minor) }))
    .filter((x) => x.value > 0)
    .sort((a, b) => b.value - a.value),
);

const money = (minor: number | null | undefined) => formatMinor(minor ?? 0, cur.value, locale.value);
const count = (n: number) => new Intl.NumberFormat(locale.value).format(n);
const pct = (v: number | null) => (v === null ? "—" : `${new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(v)}%`);
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
          <template #sub>{{ t("dash.kpi.mrrSub", { arr: money(mrr * 12), n: count(blocks.reduce((n, x) => n + (x.block?.subscriptions?.active ?? 0), 0)) }) }}</template>
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
              <StatusPill v-if="rows.find((r) => r.app.id === x.app.id)?.data" :tone="toneOf(rows.find((r) => r.app.id === x.app.id)?.data?.status)">{{ t(`product.appStatus.${rows.find((r) => r.app.id === x.app.id)?.data?.status}`) }}</StatusPill>
              <Link :to="`/app/apps/${x.app.id}`" :search="(env === 'sandbox' ? { env: 'sandbox' } : {}) as never" class="text-primary text-xs font-medium hover:underline">{{ t("dash.org.open") }}</Link>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </section>
</template>
