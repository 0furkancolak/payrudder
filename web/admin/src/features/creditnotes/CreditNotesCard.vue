<script setup lang="ts">
import { Plus, RotateCw } from "@lucide/vue";
import { Link } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { recordLink } from "@/features/related/link";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
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
  filters: { where: (n, v) => whereKey(n) === v },
});
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clear() {
  table.clear();
  range.value = { from: "", to: "" };
}
const whereFilter = computed({ get: () => table.filters.value.where ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, where: v }) });
const whereOptions = computed(() => [{ value: "", label: t("billing.creditNotes.allWhere") }, ...WHERE.map((w) => ({ value: w, label: t(`billing.creditNotes.${w}`) }))]);

type Note = (typeof list.value)[number];
const actionsOf = (n: Note) =>
  n.refund_state === "REFUND_STATE_FAILED" ? [{ key: "retry", label: t("billing.creditNotes.retryRefund"), icon: RotateCw, disabled: retry.isPending.value, onSelect: () => retry.mutate(n.id ?? "") }] : [];
const current = table.row;
</script>

<template>
  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.creditNotes.title") }}</h2>
    <ErrorText :error="retry.error.value" class="px-3" />
    <ErrorText :error="notes.error.value" class="px-3" />
    <DataToolbar v-if="list.length || issuable" v-model:search="table.search.value" :active="active" class="pt-3" @clear="clear">
      <FilterSelect v-model="whereFilter" multiple :label="t('billing.creditNotes.where')" :options="whereOptions" />
      <DateRangePicker v-model="range" />
      <template v-if="issuable" #actions>
        <Button type="button" @click="issueOpen = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.creditNotes.new") }}</Button>
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
    <p v-if="!notes.isPending.value && list.length === 0 && !notes.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.creditNotes.empty") }}</p>
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.number ?? ''" :description="current?.reason || undefined">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="credit-note-detail">
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.amount") }}</dt>
      <dd class="text-end text-lg font-semibold tabular-nums">{{ formatMinor(current.total_minor, current.currency, locale) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.invoice") }}</dt>
      <dd class="mono text-end"><Link v-if="current.invoice_id" v-bind="recordLink('invoice', current.invoice_id, tenantId)" class="hover:underline">{{ current.invoice_number || current.invoice_id.slice(0, 8) }}</Link><template v-else>—</template></dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.customer") }}</dt>
      <dd class="text-end"><Link v-if="current.customer_external_id" v-bind="recordLink('customer', current.customer_external_id, tenantId)" class="hover:underline">{{ current.customer_external_id }}</Link><template v-else>—</template></dd>
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
