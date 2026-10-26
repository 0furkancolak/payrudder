<script setup lang="ts">
import { Eye } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCustomers } from "@/features/customers/api";
import { maskEmail } from "@/shared/lib/money";
import { useTableState } from "@/shared/lib/useTableState";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import type { DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCustomerUsage, useEntitlements, useMeters, useSeries, useTopOfAll } from "./api";
import LimitBar from "./LimitBar.vue";
import { chartPoints, formatQuantity, granularityFor, limitState, mergeCustomers, totalOf, worstState, type LimitState, type Range, type UsageRow } from "./lib";
import PeriodControls from "./PeriodControls.vue";
import UsageChart from "./UsageChart.vue";

/**
 * The customers of the tenant with what each used in the billing period and where that stands against their limits. The usage comes from the rankings
 * of the meters; the limits are read for the customers on the page only (one call each), so a long list stays cheap.
 */
const props = defineProps<{ tenantId: string; effective: Range; period: string; tooLong: boolean }>();
const range = defineModel<DateRange>("range", { required: true });
const compare = defineModel<boolean>("compare", { required: true });
const { t, locale } = useI18n();

const customers = useCustomers(() => props.tenantId);
const meters = useMeters(() => props.tenantId);
const codes = computed(() => (meters.data.value ?? []).map((m) => m.code));
const unitOf = (code: string) => meters.data.value?.find((m) => m.code === code)?.unit_label ?? "";
const top = useTopOfAll(() => props.tenantId, codes, () => props.period, 100);

const rows = computed(() => mergeCustomers(customers.data.value?.customers ?? [], top.value.by));
const table = useTableState<UsageRow>(rows, {
  searchFields: (r) => [r.externalId, r.name, r.email],
  filters: {
    meter: (r, v) => v in r.usage,
    usage: (r, v) => (v === "used" ? Object.keys(r.usage).length > 0 : Object.keys(r.usage).length === 0),
  },
});
const filter = (key: string) => computed({ get: () => table.filters.value[key] ?? "", set: (v) => (table.filters.value = { ...table.filters.value, [key]: String(v) }) });
const meterFilter = filter("meter");
const usageFilter = filter("usage");
const meterOptions = computed(() => [{ value: "", label: t("billing.usage.customers.allMeters") }, ...codes.value.map((c) => ({ value: c }))]);
const usageOptions = computed(() => [
  { value: "", label: t("billing.usage.customers.anyUsage") },
  { value: "used", label: t("billing.usage.customers.used") },
  { value: "idle", label: t("billing.usage.customers.idle") },
]);

// Limits for the customers on the page, and for the one whose sheet is open.
const current = table.row;
const visible = computed(() => [...new Set([...table.items.value.map((r) => r.externalId), ...(current.value ? [current.value.externalId] : [])])]);
const ents = useEntitlements(() => props.tenantId, visible, () => props.period);
const stateOf = (id: string): LimitState | undefined => {
  const items = ents.value.by[id];
  return items === undefined ? undefined : worstState(items.map((e) => limitState(e)));
};
const tone = { over: "danger", near: "warning", ok: "success", unlimited: "neutral" } as const;
const usageText = (r: UsageRow) => {
  const entries = Object.entries(r.usage);
  if (entries.length === 0) return "";
  const shown = entries.slice(0, 2).map(([m, u]) => `${m} ${formatQuantity(u.quantity, locale.value)}`);
  return entries.length > 2 ? `${shown.join(" · ")} · +${entries.length - 2}` : shown.join(" · ");
};

// The open customer: usage and limits per meter, and the series of a meter.
const currentId = computed(() => current.value?.externalId ?? "");
const usage = useCustomerUsage(() => props.tenantId, currentId, () => props.period);
const items = computed(() => ents.value.by[currentId.value]);
const perMeter = computed(() =>
  codes.value.map((code) => {
    const e = items.value?.find((x) => x.meter_code === code);
    const used = e?.used ?? usage.data.value?.find((u) => u.meter_code === code)?.quantity ?? current.value?.usage[code]?.quantity ?? "0";
    return { code, unit: unitOf(code), e: e ?? { used, unlimited: true, allowed: true } };
  }),
);
const picked = ref("");
watch([currentId, codes], () => {
  const used = Object.keys(current.value?.usage ?? {});
  picked.value = used[0] ?? codes.value[0] ?? "";
});
const series = useSeries(() => props.tenantId, picked, () => props.effective, () => compare.value, currentId);
const pickedAggregation = computed(() => meters.data.value?.find((m) => m.code === picked.value)?.aggregation);
const pickedTotal = computed(() => (series.data.value ? totalOf(pickedAggregation.value, series.data.value.current.map((b) => b.quantity)) : undefined));
const pickedPrev = computed(() => (series.data.value?.previous ? totalOf(pickedAggregation.value, series.data.value.previous.map((b) => b.quantity)) : undefined));
const detailRows = computed(() => {
  const r = current.value;
  if (!r) return [];
  return [
    { key: "id", label: t("billing.customers.externalId"), value: r.externalId, mono: true },
    { key: "name", label: t("billing.customers.name"), value: r.name },
    { key: "email", label: t("billing.customers.email"), value: maskEmail(r.email) },
    { key: "period", label: t("billing.usage.customers.billingPeriod"), value: props.period, mono: true },
  ];
});
const error = computed(() => customers.error.value ?? meters.error.value ?? top.value.error);
</script>

<template>
  <Card class="p-3" data-testid="usage-customers">
    <ErrorText :error="error" class="px-3 pb-2" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('billing.usage.customers.search')" @clear="table.clear()">
      <FilterSelect v-model="meterFilter" :label="t('billing.usage.customers.meter')" :options="meterOptions" />
      <FilterSelect v-model="usageFilter" :label="t('billing.usage.customers.usage')" :options="usageOptions" />
      <PeriodControls v-model:range="range" v-model:compare="compare" :period="period" :too-long="tooLong" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.customers.externalId") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.customers.name") }}</TableHead>
          <TableHead>{{ t("billing.usage.customers.usage") }}</TableHead>
          <TableHead>{{ t("billing.usage.customers.limits") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in table.items.value" :key="r.externalId" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" :data-testid="`usage-customer-${r.externalId}`" @click="table.open(i)">
          <TableCell class="mono text-xs font-medium">{{ r.externalId }}</TableCell>
          <TableCell class="max-md:hidden">{{ r.name || "—" }}</TableCell>
          <TableCell class="text-sm tabular-nums">{{ usageText(r) || "—" }}</TableCell>
          <TableCell>
            <Skeleton v-if="stateOf(r.externalId) === undefined" class="h-5 w-16" />
            <StatusPill v-else :tone="tone[stateOf(r.externalId)!]" :data-state="stateOf(r.externalId)">{{ t(`billing.usage.state.${stateOf(r.externalId)}`) }}</StatusPill>
          </TableCell>
          <TableCell><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: Eye, onSelect: () => table.open(i) }]" :label="`${t('common.moreActions')} ${r.externalId}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <Skeleton v-if="customers.isPending.value" class="mx-3 my-4 h-24" />
    <p v-else-if="rows.length === 0 && !customers.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.customers.empty") }}</p>
    <p v-else-if="rows.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="rows.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.name || currentId" :description="current?.name ? currentId : undefined" wide>
    <div v-if="current" class="flex flex-col gap-6" data-testid="usage-customer-detail">
      <DetailList :rows="detailRows" />
      <section class="flex flex-col gap-3" :aria-label="t('billing.usage.customers.limits')">
        <h3 class="text-base font-semibold">{{ t("billing.usage.customers.limitsOf", { period }) }}</h3>
        <ErrorText :error="usage.error.value" />
        <p v-if="codes.length === 0" class="text-muted-foreground text-sm">{{ t("billing.usage.empty.hint") }}</p>
        <ul v-else class="flex flex-col divide-y">
          <li v-for="m in perMeter" :key="m.code" class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-3" :data-testid="`detail-meter-${m.code}`">
            <span class="mono text-sm font-medium">{{ m.code }}</span>
            <LimitBar :entitlement="m.e" :unit="m.unit" class="min-w-0 flex-1 sm:max-w-sm" />
          </li>
        </ul>
      </section>
      <section v-if="codes.length > 0" class="flex flex-col gap-3" :aria-label="t('billing.usage.meters.series')">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="text-base font-semibold">{{ t("billing.usage.meters.series") }}</h3>
          <NativeSelect v-model="picked" class="w-48" :aria-label="t('billing.usage.customers.meter')">
            <option v-for="c in codes" :key="c" :value="c">{{ c }}</option>
          </NativeSelect>
        </div>
        <ErrorText :error="series.error.value" />
        <Skeleton v-if="!series.data.value" class="h-64 w-full" />
        <UsageChart
          v-else
          :points="chartPoints(series.data.value.current, series.data.value.previous)"
          :unit="unitOf(picked)"
          :granularity="granularityFor(effective)"
          :compare="compare"
          :total="formatQuantity(pickedTotal, locale)"
          :previous-total="pickedPrev !== undefined ? formatQuantity(pickedPrev, locale) : undefined"
        />
      </section>
    </div>
  </DetailSheet>
</template>
