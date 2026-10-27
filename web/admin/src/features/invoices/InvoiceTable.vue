<script setup lang="ts">
import CustomerLabel from "@/features/customers/CustomerLabel.vue";
import { ArrowUpRight } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useTenantInvoices } from "./api";
import InvoiceDetail from "./InvoiceDetail.vue";
import { peek } from "@/features/related/peek";

/** The invoices of a tenant. A row opens the invoice in a sheet that steps through every row the filters leave, across pages. A link from another page opens an invoice with `?invoice=<id>`. */
const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const invoices = useTenantInvoices(() => props.tenantId);
const list = computed(() => invoices.data.value?.invoices ?? []);
const STATUSES = ["draft", "open", "paid", "uncollectible", "void"] as const;
const KINDS = ["REGULAR", "CORRECTION", "CREDIT_NOTE"] as const;
const range = ref<DateRange>(thisMonth());
const table = useTableState(() => list.value.filter((inv) => inRange(inv.create_time, range.value)), {
  sheet: { kind: "invoice", id: (inv) => inv.id }, searchFields: (inv) => [inv.number, inv.period_id, inv.customer_external_id, inv.total_minor],
  url: true, filters: { status: (inv, v) => inv.status === v, kind: (inv, v) => inv.kind === v },
});
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clear() {
  table.clear();
  range.value = { from: "", to: "" };
}
const setFilter = (key: string) => computed({ get: () => table.filters.value[key] ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const statusFilter = setFilter("status");
const kindFilter = setFilter("kind");
const statusOptions = computed(() => [{ value: "", label: t("tenant.allStatuses") }, ...STATUSES.map((s) => ({ value: s, label: label(t, te, "invoiceStatus", s) }))]);
const kindOptions = computed(() => [{ value: "", label: t("tenant.allKinds") }, ...KINDS.map((k) => ({ value: k, label: label(t, te, "invoiceKind", k) }))]);

const current = table.row;
// A link names an invoice: found among the rows (a filter that hides it is cleared first), or else opened on its own (see PeekHost), since the API sent only the newest ones.
useOpenParam("invoice", () => invoices.isSuccess.value, (id) => {
  const is = (inv: (typeof list.value)[number]) => inv.id === id;
  if (table.openWhere(is)) return;
  clear();
  if (!table.openWhere(is)) peek("invoice", props.tenantId, id);
});
</script>

<template>
  <ErrorState :error="invoices.error.value" :retrying="invoices.isFetching.value" compact @retry="invoices.refetch()" />
  <DataToolbar v-if="list.length" v-model:search="table.search.value" :active="active" class="pt-2" @clear="clear">
    <FilterSelect v-model="statusFilter" multiple :label="t('tenant.columns.status')" :options="statusOptions" />
    <FilterSelect v-model="kindFilter" multiple :label="t('tenant.columns.kind')" :options="kindOptions" />
    <DateRangePicker v-model="range" />
  </DataToolbar>
  <p v-if="list.length && !table.total.value" class="text-muted-foreground px-3 py-4 text-sm">{{ t("common.noMatches") }}</p>
  <Table v-else>
    <TableHeader>
      <TableRow class="hover:bg-transparent">
        <TableHead>{{ t("tenant.columns.number") }}</TableHead>
        <TableHead>{{ t("tenant.columns.kind") }}</TableHead>
        <TableHead>{{ t("tenant.columns.period") }}</TableHead>
        <TableHead>{{ t("tenant.columns.customer") }}</TableHead>
        <TableHead>{{ t("tenant.columns.created") }}</TableHead>
        <TableHead class="text-end">{{ t("tenant.columns.total") }}</TableHead>
        <TableHead>{{ t("tenant.columns.status") }}</TableHead>
        <TableHead>{{ t("tenant.columns.saga") }}</TableHead>
        <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="(inv, i) in table.items.value" :key="inv.id" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
        <TableCell class="font-medium">{{ inv.number || t("tenant.draft") }}</TableCell>
        <TableCell>{{ label(t, te, "invoiceKind", inv.kind) }}</TableCell>
        <TableCell>{{ inv.period_id }}</TableCell>
        <TableCell><CustomerLabel :id="inv.customer_external_id" :tenant-id="tenantId" /></TableCell>
        <TableCell class="whitespace-nowrap">{{ fmt.date(inv.create_time, "day", "—") }}</TableCell>
        <TableCell class="text-end font-medium tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
        <TableCell><Badge :variant="statusVariant(inv.status)">{{ label(t, te, "invoiceStatus", inv.status) }}</Badge></TableCell>
        <TableCell><Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ label(t, te, "sagaState", inv.saga_state) }}</Badge></TableCell>
        <TableCell class="text-end"><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: ArrowUpRight, onSelect: () => table.open(i) }]" /></TableCell>
      </TableRow>
    </TableBody>
  </Table>
  <DataPagination v-if="list.length" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
  <p v-if="!invoices.isPending.value && list.length === 0 && !invoices.error.value" class="text-muted-foreground p-4 text-center text-sm">
    {{ t("tenant.noInvoices") }}
  </p>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.number || t('tenant.draft')" :description="current ? [label(t, te, 'invoiceKind', current.kind), current.customer_external_id].filter(Boolean).join(' · ') : undefined" wide>
    <InvoiceDetail v-if="current" :key="current.id" :tenant-id="tenantId" :invoice-id="current.id ?? ''" />
  </DetailSheet>
</template>
