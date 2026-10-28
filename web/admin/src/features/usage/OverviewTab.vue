<script setup lang="ts">
import { ArrowDownRight, ArrowRight, Activity, ArrowUpRight, Sparkles } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import BarList from "@/features/dashboard/BarList.vue";
import { delta } from "@/features/dashboard/lib";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { Sparkline } from "@/shared/ui/sparkline";
import { StatusPill } from "@/shared/ui/status-pill";
import { useEntitlements, useMeters, useSeriesOf, useTopOfAll } from "./api";
import LimitBar from "./LimitBar.vue";
import { chartPoints, formatQuantity, granularityFor, limitState, percentUsed, totalOf, values, type Range } from "./lib";
import UsageChart from "./UsageChart.vue";

/**
 * The first look at the usage of the tenant: a card per meter with its total for the period and how it moved against the period before, the chosen meter
 * as a chart, who used it most in the billing period, and who is near or over a limit (counted from the customers the rankings name).
 */
const props = defineProps<{ tenantId: string; range: Range; compare: boolean; period: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();

const meters = useMeters(() => props.tenantId);
const codes = computed(() => (meters.data.value ?? []).map((m) => m.code));
const unitOf = (code: string) => meters.data.value?.find((m) => m.code === code)?.unit_label ?? "";

const selected = ref("");
watch(
  codes,
  (c) => {
    if (!c.includes(selected.value)) selected.value = c[0] ?? "";
  },
  { immediate: true },
);

const series = useSeriesOf(() => props.tenantId, codes, () => props.range, () => props.compare);
const cards = computed(() =>
  codes.value.map((code) => {
    const s = series.value.by[code];
    const aggregation = meters.data.value?.find((m) => m.code === code)?.aggregation;
    const total = s ? totalOf(aggregation, s.current.map((b) => b.quantity)) : undefined;
    const prev = s?.previous ? totalOf(aggregation, s.previous.map((b) => b.quantity)) : undefined;
    return { code, unit: unitOf(code), total, prev, delta: total !== undefined && total !== "" && prev !== undefined && prev !== "" ? delta(Number(total), Number(prev)) : null, spark: s ? values(s.current) : [] };
  }),
);
const chosen = computed(() => cards.value.find((c) => c.code === selected.value));
const chosenSeries = computed(() => series.value.by[selected.value]);

// Rankings and limits are counted per billing period.
const top = useTopOfAll(() => props.tenantId, codes, () => props.period, 10);
const ranking = computed(() =>
  (top.value.by[selected.value] ?? []).map((c) => ({
    key: c.customer_external_id ?? "",
    label: c.customer_external_id ?? "",
    sub: t("billing.usage.events", { n: fmt.count(Number(c.event_count ?? 0)) }),
    value: Number(c.quantity ?? 0),
    text: `${formatQuantity(c.quantity, locale.value)} ${unitOf(selected.value)}`.trim(),
  })),
);
const checked = computed(() => [...new Set(Object.values(top.value.by).flatMap((l) => l.map((c) => c.customer_external_id ?? "")).filter(Boolean))].slice(0, 40));
const ents = useEntitlements(() => props.tenantId, checked, () => props.period);
const limited = computed(() =>
  Object.entries(ents.value.by)
    .flatMap(([customer, items]) => (items ?? []).map((e) => ({ customer, e, state: limitState(e), percent: percentUsed(e.used ?? "0", e.limit) ?? 0 })))
    .filter((r) => r.state === "near" || r.state === "over")
    .sort((a, b) => b.percent - a.percent || a.customer.localeCompare(b.customer)),
);
const over = computed(() => limited.value.filter((r) => r.state === "over").length);
const near = computed(() => limited.value.filter((r) => r.state === "near").length);

const sparkOk = (v: number[]) => v.some((n) => n > 0);
const word = (d: NonNullable<(typeof cards.value)[number]["delta"]>) => (d.direction === "new" ? t("dash.delta.new") : d.percent === null ? "" : `${d.percent > 0 ? "+" : ""}${fmt.percent(d.percent)}`);
const error = computed(() => meters.error.value ?? series.value.error ?? top.value.error ?? ents.value.error);
</script>

<template>
  <div class="flex flex-col gap-6" data-testid="usage-overview">
    <ErrorText :error="error" />

    <div v-if="meters.isPending.value" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" aria-busy="true">
      <Skeleton v-for="i in 3" :key="i" class="h-36 rounded-3xl" />
    </div>
    <Card v-else-if="codes.length === 0 && !meters.error.value" class="items-center gap-2 py-12 text-center" data-testid="usage-empty">
      <Activity class="text-muted-foreground size-8" aria-hidden="true" />
      <h2 class="text-lg font-semibold">{{ t("billing.usage.empty.title") }}</h2>
      <p class="text-muted-foreground max-w-md text-sm">{{ t("billing.usage.empty.hint") }}</p>
    </Card>

    <template v-else-if="codes.length > 0">
      <ul class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" :aria-label="t('billing.usage.tabs.meters')">
        <li v-for="c in cards" :key="c.code">
          <button
            type="button"
            :aria-pressed="c.code === selected"
            :data-testid="`meter-card-${c.code}`"
            :class="['surface flex h-full min-h-36 w-full flex-col justify-between gap-3 p-5 text-start transition-shadow', c.code === selected ? 'ring-primary ring-2' : 'hover:shadow-md']"
            @click="selected = c.code"
          >
            <span class="flex items-start justify-between gap-2">
              <span class="mono text-sm font-medium">{{ c.code }}</span>
              <span
                v-if="c.delta && c.delta.direction !== 'none'"
                :class="['inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium', c.delta.direction === 'up' || c.delta.direction === 'new' ? 'bg-brand-soft text-brand-strong' : 'bg-muted text-muted-foreground']"
                :title="c.prev !== undefined ? t('billing.usage.before', { value: formatQuantity(c.prev, locale), unit: c.unit }) : undefined"
                :data-testid="`meter-delta-${c.code}`"
              >
                <Sparkles v-if="c.delta.direction === 'new'" class="size-3" aria-hidden="true" />
                <ArrowUpRight v-else-if="c.delta.direction === 'up'" class="size-3" aria-hidden="true" />
                <ArrowDownRight v-else-if="c.delta.direction === 'down'" class="size-3" aria-hidden="true" />
                <ArrowRight v-else class="size-3" aria-hidden="true" />
                {{ word(c.delta) }}
              </span>
            </span>
            <span class="flex flex-col gap-2">
              <span>
                <span class="block text-2xl font-semibold tracking-tight tabular-nums [overflow-wrap:anywhere]" :data-testid="`meter-total-${c.code}`">
                  <Skeleton v-if="c.total === undefined" class="h-7 w-24" />
                  <template v-else>{{ formatQuantity(c.total, locale) }}</template>
                </span>
                <span class="text-muted-foreground text-xs">{{ c.unit }}</span>
              </span>
              <Sparkline v-if="sparkOk(c.spark)" :values="c.spark" :width="150" :height="30" />
            </span>
          </button>
        </li>
      </ul>

      <Card v-if="chosen" data-testid="usage-chart-card">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <h2 class="text-lg font-semibold tracking-tight"><span class="mono">{{ chosen.code }}</span></h2>
          <span v-if="chosenSeries?.precision === 'estimated'" class="text-muted-foreground text-xs">{{ t("billing.usage.chart.estimate") }}</span>
        </div>
        <Skeleton v-if="!chosenSeries" class="h-64 w-full" />
        <UsageChart
          v-else
          :points="chartPoints(chosenSeries.current, chosenSeries.previous)"
          :unit="chosen.unit"
          :granularity="granularityFor(range)"
          :compare="compare"
          :total="formatQuantity(chosen.total, locale)"
          :previous-total="chosen.prev !== undefined ? formatQuantity(chosen.prev, locale) : undefined"
        />
      </Card>

      <div class="grid gap-6 lg:grid-cols-2">
        <Card data-testid="usage-top">
          <div>
            <h2 class="text-lg font-semibold tracking-tight">{{ t("billing.usage.top.title") }}</h2>
            <p class="text-muted-foreground text-sm">{{ t("billing.usage.top.hint", { meter: selected, period }) }}</p>
          </div>
          <Skeleton v-if="top.pending && ranking.length === 0" class="h-32 w-full" />
          <p v-else-if="ranking.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("billing.usage.top.empty") }}</p>
          <BarList v-else :items="ranking" />
        </Card>

        <Card data-testid="usage-limits">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 class="text-lg font-semibold tracking-tight">{{ t("billing.usage.limits.title") }}</h2>
              <p class="text-muted-foreground text-sm">{{ t("billing.usage.limits.hint", { n: checked.length, period }) }}</p>
            </div>
            <span class="flex gap-1.5">
              <StatusPill v-if="over > 0" tone="danger">{{ t("billing.usage.limits.over", { n: over }) }}</StatusPill>
              <StatusPill v-if="near > 0" tone="warning">{{ t("billing.usage.limits.near", { n: near }) }}</StatusPill>
            </span>
          </div>
          <Skeleton v-if="ents.pending && limited.length === 0" class="h-32 w-full" />
          <p v-else-if="limited.length === 0" class="text-muted-foreground py-6 text-center text-sm" data-testid="limits-clear">{{ checked.length === 0 ? t("billing.usage.limits.nobody") : t("billing.usage.limits.clear") }}</p>
          <ul v-else class="flex flex-col gap-4">
            <li v-for="r in limited.slice(0, 8)" :key="`${r.customer}-${r.e.meter_code}`" class="flex flex-col gap-1.5" :data-testid="`limit-${r.customer}-${r.e.meter_code}`">
              <p class="flex items-baseline justify-between gap-2 text-sm">
                <span class="truncate font-medium">{{ r.customer }}</span>
                <span class="mono text-muted-foreground text-xs">{{ r.e.meter_code }}</span>
              </p>
              <LimitBar :entitlement="r.e" :unit="unitOf(r.e.meter_code ?? '')" />
            </li>
          </ul>
        </Card>
      </div>
    </template>
  </div>
</template>
