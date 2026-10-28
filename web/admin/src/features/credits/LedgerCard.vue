<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { useTableState } from "@/shared/lib/useTableState";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { FilterSelect } from "@/shared/ui/filter-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreditLedger } from "./api";
import { formatCredits } from "./lib";

/** Everything that happened to one customer's credits, newest first. `bare` leaves out the card and its title, for a place that has its own (a tab). */
const props = defineProps<{ tenantId: string; customer: string; bare?: boolean }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const ledger = useCreditLedger(
  () => props.tenantId,
  () => props.customer,
);
type Entry = NonNullable<NonNullable<typeof ledger.data.value>["entries"]>[number];
const range = ref<DateRange>(thisMonth());
const table = useTableState<Entry>(() => (ledger.data.value?.entries ?? []).filter((e) => inRange(e.create_time, range.value)), {
  searchFields: (e) => [e.reason, e.kind ? t(`billing.credits.kind.${e.kind}`) : "", e.source ? t(`billing.credits.source.${e.source}`) : ""],
  filters: { kind: (e, v) => e.kind === v },
});
const kinds = computed(() => [{ value: "", label: t("common.all") }, ...["grant", "consume", "expire"].map((k) => ({ value: k, label: t(`billing.credits.kind.${k}`) }))]);
const active = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clear() {
  table.clear();
  range.value = { from: "", to: "" };
}
const kindTone = (k?: string) => (k === "grant" ? "success" : k === "consume" ? "info" : "neutral");
const kindFilter = computed({ get: () => table.filters.value.kind ?? [], set: (v: string[]) => (table.filters.value.kind = v) });
</script>

<template>
  <Card :class="bare ? 'gap-2 border-0 bg-transparent p-0 shadow-none' : 'p-3'" data-testid="credits-ledger">
    <h2 v-if="!bare" class="px-3 pt-2 text-base font-semibold">{{ t("billing.credits.ledger.title") }}</h2>
    <DataToolbar v-model:search="table.search.value" :active="active" @clear="clear">
      <FilterSelect v-model="kindFilter" multiple :label="t('billing.credits.ledger.kind')" :options="kinds" />
      <DateRangePicker v-model="range" />
    </DataToolbar>
    <ErrorText :error="ledger.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.ledger.time") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.kind") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.source") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.reason") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.ledger.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in table.items.value" :key="e.id">
          <TableCell class="whitespace-nowrap">{{ fmt.date(e.create_time, "dateTime") }}</TableCell>
          <TableCell><StatusPill :tone="kindTone(e.kind)">{{ t(`billing.credits.kind.${e.kind}`) }}</StatusPill></TableCell>
          <TableCell>
            <StatusPill v-if="e.source === 'purchase'" tone="success">{{ t("billing.credits.source.purchase") }}</StatusPill>
            <span v-else-if="e.source" class="text-muted-foreground text-sm">{{ t(`billing.credits.source.${e.source}`) }}</span>
          </TableCell>
          <TableCell class="text-muted-foreground">{{ e.reason }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatCredits(e.amount, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <TableEmpty v-if="ledger.data.value && !(ledger.data.value.entries ?? []).length">{{ t("billing.credits.ledger.none") }}</TableEmpty>
    <TableEmpty v-else-if="table.total.value === 0 && active">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
