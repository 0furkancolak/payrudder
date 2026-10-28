<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { ArrowUpRight, GitBranchPlus } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import BarList from "@/features/dashboard/BarList.vue";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import type { DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { Sparkline } from "@/shared/ui/sparkline";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useMeters, useSeriesOf, useTopCustomers, type Meter } from "./api";
import { chartPoints, formatQuantity, granularityFor, totalOf, values, type Range } from "./lib";
import { AGGREGATIONS, aggregationKey } from "./meter";
import MeterSheet from "./MeterSheet.vue";
import PeriodControls from "./PeriodControls.vue";
import UsageChart from "./UsageChart.vue";

/** The meters of the tenant as a table: what each counts, and how much it counted in the period. A row opens the meter with its series and its heaviest users. */
const props = defineProps<{ tenantId: string; effective: Range; period: string; tooLong: boolean }>();
const range = defineModel<DateRange>("range", { required: true });
const compare = defineModel<boolean>("compare", { required: true });
const { t, locale } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

const meters = useMeters(() => props.tenantId);
const list = computed<Meter[]>(() => meters.data.value ?? []);
const table = useTableState<Meter>(list, {
  searchFields: (m) => [m.code, m.unit_label, m.value_property],
  filters: { aggregation: (m, v) => m.aggregation === v },
});
const aggregation = computed({ get: () => table.filters.value.aggregation ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, aggregation: v }) });
const aggregationOptions = computed(() => [{ value: "", label: t("billing.usage.meters.allAggregations") }, ...AGGREGATIONS.map((a) => ({ value: a, label: t(`billing.usage.aggregation.${aggregationKey(a)}`) }))]);

const codes = computed(() => list.value.map((m) => m.code));
const series = useSeriesOf(() => props.tenantId, codes, () => props.effective, () => compare.value);
const periodTotal = (code: string) => {
  const s = series.value.by[code];
  return s ? totalOf(list.value.find((m) => m.code === code)?.aggregation, s.current.map((b) => b.quantity)) : undefined;
};

const current = table.row;
const detailSeries = computed(() => (current.value ? series.value.by[current.value.code] : undefined));
const detailTotal = computed(() => (current.value ? periodTotal(current.value.code) : undefined));
const detailPrev = computed(() => (detailSeries.value?.previous ? totalOf(current.value?.aggregation, detailSeries.value.previous.map((b) => b.quantity)) : undefined));
const top = useTopCustomers(() => props.tenantId, () => current.value?.code ?? "", () => props.period, 10);
const ranking = computed(() =>
  (top.data.value ?? []).map((c) => ({
    key: c.customer_external_id ?? "",
    label: c.customer_external_id ?? "",
    sub: t("billing.usage.events", { n: fmt.count(Number(c.event_count ?? 0)) }),
    value: Number(c.quantity ?? 0),
    text: `${formatQuantity(c.quantity, locale.value)} ${current.value?.unit_label ?? ""}`.trim(),
  })),
);
const detailRows = computed(() => {
  const m = current.value;
  if (!m) return [];
  return [
    { key: "code", label: t("billing.usage.meters.code"), value: m.code, mono: true },
    { key: "unit", label: t("billing.usage.meters.unit"), value: m.unit_label },
    { key: "aggregation", label: t("billing.usage.meters.aggregation"), value: t(`billing.usage.aggregation.${aggregationKey(m.aggregation)}`) },
    { key: "property", label: t("billing.usage.meters.property"), value: m.value_property, mono: true },
    { key: "scale", label: t("billing.usage.meters.scale"), value: m.quantity_scale ?? 0 },
    { key: "filter", label: t("billing.usage.meters.filterShort"), value: filterText(m) },
    { key: "version", label: t("billing.usage.meters.version"), value: m.version },
    { key: "created", label: t("billing.usage.meters.created"), value: fmt.date(m.create_time, "dateTime", "—") },
  ];
});
const filterText = (m: Meter) =>
  Object.entries(m.filter ?? {})
    .map(([k, f]) => `${k} = ${(f.values ?? []).join(" | ")}`)
    .join("; ");

// The form: a new meter, or the next version of one.
const sheet = ref(false);
const base = ref<Meter | null>(null);
function openForm(m: Meter | null) {
  base.value = m;
  sheet.value = true;
}
useSheetTarget("meter-version", sheet, base, { id: (m) => m.code ?? "", find: (code) => list.value.find((m) => m.code === code), restore: openForm });
const actionsOf = (m: Meter, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) },
  { key: "version", label: t("billing.usage.meters.newVersion"), icon: GitBranchPlus, disabled: !canWrite.value, onSelect: () => openForm(m) },
];
</script>

<template>
  <Card class="p-3" data-testid="usage-meters">
    <ErrorState :error="meters.error.value" :retrying="meters.isFetching.value" compact @retry="meters.refetch()" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('billing.usage.meters.search')" @clear="table.clear()">
      <FilterSelect v-model="aggregation" multiple :label="t('billing.usage.meters.aggregation')" :options="aggregationOptions" />
      <PeriodControls v-model:range="range" v-model:compare="compare" :period="period" :too-long="tooLong" />
      <HeaderAction test-id="meters-new" :disabled="!canWrite" :label="t('billing.usage.meters.new')" @click="openForm(null)" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.usage.meters.code") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.usage.meters.aggregation") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.usage.meters.property") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.usage.meters.inPeriod") }}</TableHead>
          <TableHead class="max-lg:hidden"><span class="sr-only">{{ t("billing.usage.meters.trend") }}</span></TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(m, i) in table.items.value" :key="m.code" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" :data-testid="`meter-${m.code}`" @click="table.open(i)">
          <TableCell>
            <span class="mono text-xs font-medium">{{ m.code }}</span>
            <span class="text-muted-foreground block text-xs">{{ m.unit_label }} · v{{ m.version }}</span>
          </TableCell>
          <TableCell class="max-md:hidden">{{ t(`billing.usage.aggregation.${aggregationKey(m.aggregation)}`) }}</TableCell>
          <TableCell class="mono text-xs max-md:hidden">{{ m.value_property || "—" }}</TableCell>
          <TableCell class="text-end tabular-nums">
            <Skeleton v-if="periodTotal(m.code) === undefined" class="ms-auto h-4 w-16" />
            <template v-else>{{ formatQuantity(periodTotal(m.code), locale) }} <span class="text-muted-foreground text-xs">{{ m.unit_label }}</span></template>
          </TableCell>
          <TableCell class="max-lg:hidden"><Sparkline v-if="series.by[m.code]" :values="values(series.by[m.code]!.current)" :width="90" :height="24" /></TableCell>
          <TableCell><RowActions :actions="actionsOf(m, i)" :label="`${t('common.moreActions')} ${m.code}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <Skeleton v-if="meters.isPending.value" class="mx-3 my-4 h-24" />
    <TableEmpty v-else-if="list.length === 0 && !meters.error.value" data-testid="meters-empty">{{ t("billing.usage.empty.hint") }}</TableEmpty>
    <TableEmpty v-else-if="list.length > 0 && table.total.value === 0">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.code ?? ''" :description="current ? `${current.unit_label} · ${t(`billing.usage.aggregation.${aggregationKey(current.aggregation)}`)}` : undefined" wide>
    <div v-if="current" class="flex flex-col gap-6" data-testid="meter-detail">
      <DetailList :rows="detailRows" />
      <section class="flex flex-col gap-3" :aria-label="t('billing.usage.meters.series')">
        <h3 class="text-base font-semibold">{{ t("billing.usage.meters.series") }}</h3>
        <Skeleton v-if="!detailSeries" class="h-64 w-full" />
        <UsageChart
          v-else
          :points="chartPoints(detailSeries.current, detailSeries.previous)"
          :unit="current.unit_label"
          :granularity="granularityFor(effective)"
          :compare="compare"
          :total="formatQuantity(detailTotal, locale)"
          :previous-total="detailPrev !== undefined ? formatQuantity(detailPrev, locale) : undefined"
        />
      </section>
      <section class="flex flex-col gap-3" :aria-label="t('billing.usage.top.title')">
        <h3 class="text-base font-semibold">{{ t("billing.usage.top.title") }}</h3>
        <p class="text-muted-foreground -mt-2 text-sm">{{ t("billing.usage.top.hint", { meter: current.code, period }) }}</p>
        <ErrorText :error="top.error.value" />
        <Skeleton v-if="top.isPending.value" class="h-24 w-full" />
        <p v-else-if="ranking.length === 0" class="text-muted-foreground text-sm">{{ t("billing.usage.top.empty") }}</p>
        <BarList v-else :items="ranking" />
      </section>
    </div>
    <template #footer>
      <Button type="button" variant="outline" :disabled="!canWrite" @click="openForm(current ?? null)"><GitBranchPlus class="size-4" aria-hidden="true" />{{ t("billing.usage.meters.newVersion") }}</Button>
    </template>
  </DetailSheet>

  <MeterSheet v-model:open="sheet" :tenant-id="tenantId" :base="base" />
</template>
