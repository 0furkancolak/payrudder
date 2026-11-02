<script setup lang="ts">
import { ArrowUpRight } from "@lucide/vue";

import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { inRange, rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { useTableState } from "@/shared/lib/useTableState";
import { SortSelect } from "@/shared/ui/sort-select";
import { amount, amountHigh, amountLow, newest, oldest, time } from "@/shared/lib/sortDefs";
import { ErrorState } from "@/shared/ui/error-state";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import CreditNotesCard from "@/features/creditnotes/CreditNotesCard.vue";
import { PaymentContext } from "@/features/related";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { useLedger } from "./api";
import ReconcileQueue from "./ReconcileQueue.vue";
import SettlementCard from "./SettlementCard.vue";
import FeeRulesCard from "./FeeRulesCard.vue";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

const { t, te, locale } = useI18n();
const fmt = useFormat();
const tenantId = useRouteTenantId();
const ledger = useLedger(tenantId);

const when = (v?: string) => fmt.date(v, "short");
const all = computed(() => ({ value: "", label: t("common.all") }));
const distinct = (pick: (e: Entry) => string | undefined) => [...new Set((ledger.data.value?.entries ?? []).map((e) => pick(e) ?? "").filter(Boolean))];

// --- the ledger
type Entry = NonNullable<NonNullable<typeof ledger.data.value>["entries"]>[number];
const range = ref<DateRange>(thisMonth());
const table = useTableState<Entry>(() => (ledger.data.value?.entries ?? []).filter((e) => inRange(e.occurred_at, range.value)), {
  searchFields: (e) => [e.provider, e.provider_ref, e.kind, e.source],
  sorts: () => [newest(t, (e) => time(e.occurred_at)), oldest(t, (e) => time(e.occurred_at)), amountHigh(t, (e) => amount(e.amount_minor)), amountLow(t, (e) => amount(e.amount_minor))],
  url: true, filters: { status: (e, v) => e.status === v, provider: (e, v) => e.provider === v },
});
const statusFilter = computed({ get: () => table.filters.value.status ?? [], set: (v: string[]) => (table.filters.value.status = v) });
const providerFilter = computed({ get: () => table.filters.value.provider ?? [], set: (v: string[]) => (table.filters.value.provider = v) });
const statuses = computed(() => [all.value, ...distinct((e) => e.status).map((s) => ({ value: s, label: label(t, te, "ledgerStatus", s) }))]);
const providers = computed(() => [all.value, ...distinct((e) => e.provider).map((p) => ({ value: p, label: label(t, te, "provider", p) }))]);
const ledgerActive = computed(() => table.active.value || rangeChanged(range.value));
function clearLedger() {
  table.clear();
  range.value = thisMonth();
}

const current = table.row;
const detailRows = computed(() => {
  const e = current.value;
  if (!e) return [];
  return [
    { key: "when", label: t("billing.payments.when"), value: when(e.occurred_at) },
    { key: "kind", label: t("billing.payments.kind"), value: label(t, te, "ledgerKind", e.kind) },
    { key: "status", label: t("billing.payments.status") },
    { key: "provider", label: t("billing.payments.provider"), value: label(t, te, "provider", e.provider) },
    { key: "amount", label: t("billing.payments.amount"), value: formatMinor(String(e.amount_minor ?? 0), e.currency, locale.value) },
    { key: "reference", label: t("billing.payments.reference"), value: e.provider_ref, mono: true },
    { key: "source", label: t("billing.payments.source"), value: label(t, te, "ledgerSource", e.source) },
    { key: "id", label: t("billing.payments.id"), value: String(e.id ?? ""), mono: true },
  ];
});
// A link from an invoice or a customer opens the payment it names (the ledger row's id); a date range that hides it is cleared first.
useOpenParam("payment", () => !ledger.isPending.value, (id) => {
  const is = (e: Entry) => String(e.id) === id || e.provider_ref === id;
  if (!table.openWhere(is)) {
    clearLedger();
    table.openWhere(is);
  }
});
const rowActions = (i: number) => [{ key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) }];
</script>

<template>
  <Card class="mb-6 p-3">
    <ErrorState :error="ledger.error.value" :retrying="ledger.isFetching.value" compact @retry="ledger.refetch()" />
    <DataToolbar v-model:search="table.search.value" :active="ledgerActive" @clear="clearLedger">
      <FilterSelect v-model="statusFilter" multiple :label="t('billing.payments.status')" :options="statuses" />
      <FilterSelect v-model="providerFilter" multiple :label="t('billing.payments.provider')" :options="providers" />
      <DateRangePicker v-model="range" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.payments.reference") }}</TableHead>
          <TableHead>{{ t("billing.payments.when") }}</TableHead>
          <TableHead>{{ t("billing.payments.kind") }}</TableHead>
          <TableHead>{{ t("billing.payments.status") }}</TableHead>
          <TableHead>{{ t("billing.payments.provider") }}</TableHead>
          <TableHead>{{ t("billing.payments.source") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.payments.amount") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(e, i) in table.items.value" :key="e.id" class="cursor-pointer" @click="table.open(i)">
          <TableCell class="mono text-xs font-medium">{{ e.provider_ref || String(e.id ?? "") }}</TableCell>
          <TableCell class="whitespace-nowrap">{{ when(e.occurred_at) }}</TableCell>
          <TableCell>{{ label(t, te, "ledgerKind", e.kind) }}</TableCell>
          <TableCell><StatusPill :tone="toneOf(e.status)">{{ label(t, te, "ledgerStatus", e.status) }}</StatusPill></TableCell>
          <TableCell>{{ label(t, te, "provider", e.provider) }}</TableCell>
          <TableCell>{{ label(t, te, "ledgerSource", e.source) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(String(e.amount_minor ?? 0), e.currency, locale) }}</TableCell>
          <TableCell><RowActions :actions="rowActions(i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <TableEmpty v-if="!ledger.isPending.value && (ledger.data.value?.entries ?? []).length === 0 && !ledger.error.value">
      {{ t("billing.payments.empty") }}
    </TableEmpty>
    <TableEmpty v-else-if="table.total.value === 0 && ledgerActive">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.provider_ref || String(current?.id ?? '') || ''" :description="current ? when(current.occurred_at) : ''">
    <DetailList :rows="detailRows">
      <template #status><StatusPill :tone="toneOf(current?.status)">{{ label(t, te, "ledgerStatus", current?.status) }}</StatusPill></template>
    </DetailList>
    <PaymentContext v-if="current?.invoice_id" :key="current.id" :tenant-id="tenantId" :invoice-id="current.invoice_id" class="mt-6" />
  </DetailSheet>

  <ReconcileQueue :tenant-id="tenantId" />

  <SettlementCard :tenant-id="tenantId" />

  <FeeRulesCard :tenant-id="tenantId" />

  <CreditNotesCard :tenant-id="tenantId" />
</template>
