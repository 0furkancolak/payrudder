<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { matchesSearch } from "@/shared/lib/paging";
import { Card } from "@/shared/ui/card";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { Eye } from "@lucide/vue";
import InvoiceDetail from "./InvoiceDetail.vue";
import InvoiceSheetById from "./InvoiceSheetById.vue";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useInvoices } from "./billingApi";

/** The environment's invoices: number, customer, period, total (in its own currency), status. A row opens the invoice. Reads /v1/invoices only. */
const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const status = ref("");
const invoices = useInvoices(tenantId, status);

/** The API hands out invoices a page at a time; `at` is the page shown, and going past the last one loaded fetches the next. */
const at = ref(0);
const search = ref("");
watch(status, () => (at.value = 0));
const loaded = computed(() => invoices.data.value?.pages ?? []);
const range = ref<DateRange>(thisMonth());
const rows = computed(() => (loaded.value[at.value]?.invoices ?? []).filter((i) => matchesSearch([i.number, i.customer_external_id, i.period_id], search.value) && inRange(i.create_time, range.value)));
const filtering = computed(() => search.value !== "" || status.value !== "" || range.value.from !== "" || range.value.to !== "");
function clear() {
  search.value = "";
  status.value = "";
  range.value = { from: "", to: "" };
}
const hasNext = computed(() => at.value + 1 < loaded.value.length || !!invoices.hasNextPage.value);
async function next() {
  if (at.value + 1 >= loaded.value.length) await invoices.fetchNextPage();
  at.value = Math.min(at.value + 1, Math.max(0, loaded.value.length - 1));
}
/** The invoice whose sheet is open: an index into the rows shown now; the arrows step through them and on into the next and previous page. */
const nav = useEdgeDetail(rows, { hasPrev: () => at.value > 0, hasNext, prev: () => at.value--, next });
const shown = nav.index;
const current = nav.row;
// The arrows turn the page themselves; any other change of what is listed closes the sheet.
watch([search, status, range], () => (shown.value = null), { deep: true });
// A link names an invoice: found in the pages loaded so far (a search or date range that hides it is cleared first), or else opened on its own.
const alone = ref("");
useOpenParam("invoice", () => invoices.isSuccess.value, (id) => {
  const where = loaded.value.findIndex((p) => (p.invoices ?? []).some((i) => i.id === id));
  if (where < 0) {
    alone.value = id;
    return;
  }
  search.value = "";
  range.value = { from: "", to: "" };
  at.value = where;
  // After the watcher that closes the sheet when what is listed changes has run.
  void nextTick(() => (shown.value = rows.value.findIndex((i) => i.id === id)));
});
const statuses = ["draft", "open", "paid", "uncollectible", "void"] as const;
const day = (v?: string) => fmt.date(v, "day");
</script>

<template>
  <Card class="p-3" :aria-label="t('product.billing.invoices.title')" role="region">
    <DataToolbar v-model:search="search" :active="filtering" @clear="clear()">
      <FilterSelect v-model="status" :label="t('product.billing.invoices.status')" :options="[{ value: '', label: t('product.billing.invoices.all') }, ...statuses.map((s) => ({ value: s, label: t(`product.invoiceStatus.${s}`) }))]" />
      <DateRangePicker v-model="range" />
    </DataToolbar>

    <ErrorText :error="invoices.error.value" class="bg-destructive-soft mx-3 rounded-2xl px-4 py-3" />
    <Skeleton v-if="invoices.isPending.value" class="mx-3 h-40 rounded-2xl" />
    <template v-else>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("product.billing.invoices.number") }}</TableHead>
            <TableHead>{{ t("product.billing.invoices.customer") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("product.billing.invoices.period") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("product.billing.invoices.created") }}</TableHead>
            <TableHead>{{ t("product.billing.invoices.status") }}</TableHead>
            <TableHead class="text-end">{{ t("product.billing.invoices.total") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(inv, i) in rows" :key="inv.id" class="cursor-pointer" :data-testid="`invoice-${inv.number || inv.id}`" @click="shown = i">
            <TableCell class="font-medium whitespace-nowrap">{{ inv.number || t("product.billing.invoices.draft") }}</TableCell>
            <TableCell>{{ inv.customer_external_id }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-md:hidden">{{ inv.period_id }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-md:hidden">{{ day(inv.create_time) }}</TableCell>
            <TableCell><StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill></TableCell>
            <TableCell class="text-end font-medium whitespace-nowrap tabular">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
            <TableCell><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: Eye, onSelect: () => (shown = i) }]" :label="`${t('common.moreActions')} ${inv.number ?? ''}`" /></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="rows.length === 0 && !invoices.error.value" class="text-muted-foreground py-10 text-center text-sm">{{ t(filtering ? "common.noMatches" : "product.billing.invoices.empty") }}</p>
    </template>
    <DataPagination :page="at + 1" :has-prev="at > 0" :has-next="hasNext" @prev="at--" @next="next" />
  </Card>

  <DetailSheet v-model:index="shown" :total="rows.length" :title="current ? current.number || t('product.billing.invoices.draft') : ''" :description="current?.customer_external_id" wide :edge="nav.edge.value" @edge="nav.onEdge">
    <InvoiceDetail v-if="current?.id" :key="current.id" :tenant-id="tenantId" :invoice-id="current.id" />
  </DetailSheet>
  <InvoiceSheetById v-if="alone" :tenant-id="tenantId" :invoice-id="alone" @close="alone = ''" />
</template>
