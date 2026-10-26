<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { NativeSelect } from "@/shared/ui/native-select";
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
const date = (v?: string) => fmt.date(v, "day", "—");
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

const table = useTableState(list, {
  searchFields: (n) => [n.number, n.invoice_number, n.customer_external_id, n.reason],
  filters: { where: (n, v) => whereKey(n) === v },
});
const whereFilter = computed({ get: () => table.filters.value.where ?? "", set: (v: string) => (table.filters.value = { ...table.filters.value, where: v }) });
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="flex flex-wrap items-center justify-between gap-2 px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.creditNotes.title") }}</h2>
      <Button v-if="issuable" type="button" @click="issueOpen = true">{{ t("billing.creditNotes.new") }}</Button>
    </div>
    <ErrorText :error="retry.error.value" class="px-3" />
    <ErrorText :error="notes.error.value" class="px-3" />
    <DataToolbar v-if="list.length" v-model:search="table.search.value" :active="table.active.value" class="pt-3" @clear="table.clear()">
      <NativeSelect v-model="whereFilter" class="w-48" :aria-label="t('billing.creditNotes.where')">
        <option value="">{{ t("billing.creditNotes.where") }}</option>
        <option v-for="w in WHERE" :key="w" :value="w">{{ t(`billing.creditNotes.${w}`) }}</option>
      </NativeSelect>
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
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="n in table.items.value" :key="n.id">
          <TableCell class="mono font-medium">{{ n.number }}</TableCell>
          <TableCell v-if="!invoiceId" class="mono">{{ n.invoice_number }}</TableCell>
          <TableCell>{{ n.customer_external_id }}</TableCell>
          <TableCell>{{ n.reason }}</TableCell>
          <TableCell>
            {{ where(n) }}
            <template v-if="n.refund_state === 'REFUND_STATE_FAILED'">
              <span v-if="n.refund_error" class="text-muted-foreground block text-xs">{{ n.refund_error }}</span>
              <Button size="sm" variant="outline" :disabled="retry.isPending.value" @click="retry.mutate(n.id ?? '')">{{ t("billing.creditNotes.retryRefund") }}</Button>
            </template>
          </TableCell>
          <TableCell><EInvoiceStatus :tenant-id="tenantId" :credit-note-id="n.id ?? ''" /></TableCell>
          <TableCell>{{ date(n.create_time) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(n.total_minor, n.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.length" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
    <p v-if="!notes.isPending.value && list.length === 0 && !notes.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.creditNotes.empty") }}</p>
  </Card>
  <IssueCreditNoteSheet v-if="issuable" v-model:open="issueOpen" :tenant-id="tenantId" :invoice-id="invoiceId ?? ''" :currency="currency" />
</template>
