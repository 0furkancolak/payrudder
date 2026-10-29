<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import PeekInvoice from "@/features/related/PeekInvoice.vue";
import { Plus, RotateCw } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { peek } from "@/features/related/peek";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { SortSelect } from "@/shared/ui/sort-select";
import { amount, amountHigh, amountLow, newest, oldest, time } from "@/shared/lib/sortDefs";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { inRange, rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import EInvoiceStatus from "@/features/einvoice/EInvoiceStatus.vue";
import { useCreditNotes, useRetryCreditNoteRefund } from "./api";
import IssueCreditNoteSheet from "./IssueCreditNoteSheet.vue";

/** Credit notes of a tenant, or of one invoice; with `issuable` a paid invoice can be credited here. */
const props = defineProps<{ tenantId: string; invoiceId?: string; currency?: string; issuable?: boolean }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const notes = useCreditNotes(
  () => props.tenantId,
  () => props.invoiceId ?? "",
);
const retry = useRetryCreditNoteRefund(() => props.tenantId);
const issueOpen = ref(false);
const date = (v?: string | null) => fmt.date(v, "day", "—");
const list = computed(() => notes.data.value?.credit_notes ?? []);
type Where = "toCredit" | "refunded" | "refundFailed" | "refundRequested";
const WHERE: Where[] = ["toCredit", "refunded", "refundFailed", "refundRequested"];
/** Where a note's amount went. */
function whereKey(n: { disposition?: string; refund_state?: string }): Where {
  if (n.disposition !== "CREDIT_NOTE_DISPOSITION_REFUND") return "toCredit";
  switch (n.refund_state) {
    case "REFUND_STATE_REFUNDED":
      return "refunded";
    case "REFUND_STATE_FAILED":
      return "refundFailed";
    default:
      return "refundRequested";
  }
}
const where = (n: { disposition?: string; refund_state?: string }) => t(`billing.creditNotes.${whereKey(n)}`);

const range = ref<DateRange>(thisMonth());
const table = useTableState(() => list.value.filter((n) => inRange(n.create_time, range.value)), {
  sheet: { kind: "creditnote", id: (n) => n.id }, searchFields: (n) => [n.number, n.invoice_number, n.customer_external_id, n.reason],
  sorts: () => [newest(t, (n) => time(n.create_time)), oldest(t, (n) => time(n.create_time)), amountHigh(t, (n) => amount(n.total_minor)), amountLow(t, (n) => amount(n.total_minor))],
  filters: { where: (n, v) => whereKey(n) === v },
});
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || rangeChanged(range.value));
function clear() {
  table.clear();
  range.value = thisMonth();
}
const whereFilter = computed({ get: () => table.filters.value.where ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, where: v }) });
const whereOptions = computed(() => [{ value: "", label: t("billing.creditNotes.allWhere") }, ...WHERE.map((w) => ({ value: w, label: t(`billing.creditNotes.${w}`) }))]);

type Note = (typeof list.value)[number];
const actionsOf = (n: Note) =>
  n.refund_state === "REFUND_STATE_FAILED" ? [{ key: "retry", label: t("billing.creditNotes.retryRefund"), icon: RotateCw, disabled: retry.isPending.value, onSelect: () => retry.mutate(n.id ?? "") }] : [];
const current = table.row;
</script>

<template>
  <Card :class="invoiceId ? 'gap-3 rounded-none border-0 bg-transparent p-0 shadow-none' : 'mb-6 p-3'">
    <h2 :class="invoiceId ? 'text-lg font-semibold tracking-tight' : 'px-3 pt-2 text-lg font-semibold'">{{ t("billing.creditNotes.title") }}</h2>
    <ErrorText :error="retry.error.value" class="px-3" />
    <ErrorText :error="notes.error.value" class="px-3" />
    <DataToolbar v-if="list.length || issuable" v-model:search="table.search.value" :active="active" @clear="clear">
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      <FilterSelect v-model="whereFilter" multiple :label="t('billing.creditNotes.where')" :options="whereOptions" />
      <DateRangePicker v-model="range" />
      <!-- On a page the button is the page's (the header's); inside an invoice's sheet the header is behind the sheet, so the button is in the card. -->
      <HeaderAction v-if="issuable && !invoiceId" :label="t('billing.creditNotes.new')" @click="issueOpen = true" />
      <template v-if="issuable && invoiceId" #actions>
        <Button type="button" size="sm" @click="issueOpen = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.creditNotes.new") }}</Button>
      </template>
    </DataToolbar>
    <p v-if="list.length && !table.total.value" class="text-muted-foreground px-3 py-2 text-sm">{{ t("common.noMatches") }}</p>
    <Table v-else-if="list.length">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.creditNotes.number") }}</TableHead>
          <TableHead v-if="!invoiceId">{{ t("billing.creditNotes.invoice") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.customer") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.reason") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.where") }}</TableHead>
          <TableHead>{{ t("einvoice.title") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.date") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.creditNotes.amount") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(n, i) in table.items.value" :key="n.id" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
          <TableCell class="mono font-medium">{{ n.number }}</TableCell>
          <TableCell v-if="!invoiceId" class="mono">{{ n.invoice_number }}</TableCell>
          <TableCell>{{ n.customer_external_id }}</TableCell>
          <TableCell>{{ n.reason }}</TableCell>
          <TableCell>
            {{ where(n) }}
            <span v-if="n.refund_state === 'REFUND_STATE_FAILED' && n.refund_error" class="text-muted-foreground block text-xs">{{ n.refund_error }}</span>
          </TableCell>
          <TableCell @click.stop><EInvoiceStatus :tenant-id="tenantId" :credit-note-id="n.id ?? ''" /></TableCell>
          <TableCell>{{ date(n.create_time) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(n.total_minor, n.currency, locale) }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="actionsOf(n)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.length" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
    <TableEmpty v-if="!notes.isPending.value && list.length === 0 && !notes.error.value">{{ t("billing.creditNotes.empty") }}</TableEmpty>
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.number ?? ''" :description="current?.reason || undefined">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="credit-note-detail">
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.amount") }}</dt>
      <dd class="text-end text-lg font-semibold tabular-nums">{{ formatMinor(current.total_minor, current.currency, locale) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.invoice") }}</dt>
      <dd class="mono text-end"><PeekInvoice v-if="current.invoice_id" :id="current.invoice_id" :tenant-id="tenantId" class="hover:underline">{{ current.invoice_number || current.invoice_id.slice(0, 8) }}</PeekInvoice><template v-else>—</template></dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.customer") }}</dt>
      <dd class="text-end"><button v-if="current.customer_external_id" type="button" class="cursor-pointer hover:underline" @click="peek('customer', tenantId, current.customer_external_id)">{{ current.customer_external_id }}</button><template v-else>—</template></dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.date") }}</dt>
      <dd class="text-end">{{ date(current.create_time) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.where") }}</dt>
      <dd class="text-end">{{ where(current) }}<span v-if="current.refund_error" class="text-muted-foreground block text-xs">{{ current.refund_error }}</span></dd>
      <dt class="text-muted-foreground">{{ t("einvoice.title") }}</dt>
      <dd class="flex justify-end"><EInvoiceStatus :tenant-id="tenantId" :credit-note-id="current.id ?? ''" /></dd>
    </dl>
    <template v-if="current && current.refund_state === 'REFUND_STATE_FAILED'" #footer>
      <Button type="button" variant="outline" :disabled="retry.isPending.value" @click="retry.mutate(current.id ?? '')">{{ t("billing.creditNotes.retryRefund") }}</Button>
    </template>
  </DetailSheet>
  <IssueCreditNoteSheet v-if="issuable" v-model:open="issueOpen" :tenant-id="tenantId" :invoice-id="invoiceId ?? ''" :currency="currency" />
</template>
