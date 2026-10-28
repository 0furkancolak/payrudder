<script setup lang="ts">
import CustomerLabel from "@/features/customers/CustomerLabel.vue";

import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorState } from "@/shared/ui/error-state";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { matchesSearch } from "@/shared/lib/paging";
import { Card } from "@/shared/ui/card";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { peek } from "@/features/related/peek";
import { setKind } from "@/shared/lib/sheets";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { inRange, rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { ArrowUpRight } from "@lucide/vue";
import InvoiceDetail from "./InvoiceDetail.vue";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useInvoices } from "./billingApi";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

/** The environment's invoices: number, customer, period, total (in its own currency), status. A row opens the invoice. Reads /v1/invoices only. */
const { t, locale } = useI18n();
const fmt = useFormat();
const tenantId = useRouteTenantId();
const status = ref("");
const invoices = useInvoices(tenantId, status);

/** The API hands out invoices a page at a time; `at` is the page shown, and going past the last one loaded fetches the next. */
const at = ref(0);
const search = ref("");
watch(status, () => (at.value = 0));
const loaded = computed(() => invoices.data.value?.pages ?? []);
const range = ref<DateRange>(thisMonth());
const rows = computed(() => (loaded.value[at.value]?.invoices ?? []).filter((i) => matchesSearch([i.number, i.customer_external_id, i.period_id], search.value) && inRange(i.create_time, range.value)));
const filtering = computed(() => search.value !== "" || status.value !== "" || rangeChanged(range.value));
function clear() {
  search.value = "";
  status.value = "";
  range.value = thisMonth();
}
const hasNext = computed(() => at.value + 1 < loaded.value.length || !!invoices.hasNextPage.value);
async function next() {
  if (at.value + 1 >= loaded.value.length) await invoices.fetchNextPage();
  at.value = Math.min(at.value + 1, Math.max(0, loaded.value.length - 1));
}
/** The invoice whose sheet is open: an index into the rows shown now; the arrows step through them and on into the next and previous page. */
const nav = useEdgeDetail(
  rows,
  { hasPrev: () => at.value > 0, hasNext, prev: () => at.value--, next },
  // The address names an invoice the rows shown do not hold (a search or date range hides it, or it is on a page not loaded): see openInvoice.
  { kind: "invoice", id: (i) => i.id ?? "", ready: () => invoices.isSuccess.value, missing: (id) => (openInvoice(id), true) },
);
const shown = nav.index;
const current = nav.row;
// The arrows turn the page themselves; any other change of what is listed closes the sheet.
watch([search, status, range], () => (shown.value = null), { deep: true });
// A link or the address names an invoice: found in the pages loaded so far (a search or date range that hides it is cleared first), or else opened on its own.
function openInvoice(id: string) {
  const where = loaded.value.findIndex((p) => (p.invoices ?? []).some((i) => i.id === id));
  if (where < 0) {
    // The sheet of the invoice on its own (see PeekHost), not a row of this list: it takes the place of the name the row had in the address.
    setKind("invoice", null);
    peek("invoice", tenantId.value, id);
    return;
  }
  search.value = "";
  range.value = thisMonth();
  at.value = where;
  // After the watcher that closes the sheet when what is listed changes has run.
  void nextTick(() => (shown.value = rows.value.findIndex((i) => i.id === id)));
}
useOpenParam("invoice", () => invoices.isSuccess.value, openInvoice);
const statuses = ["draft", "open", "paid", "uncollectible", "void"] as const;
const day = (v?: string | null) => fmt.date(v, "day");
</script>

<template>
  <Card class="p-3" :aria-label="t('product.billing.invoices.title')" role="region">
    <DataToolbar v-model:search="search" :active="filtering" @clear="clear()">
      <FilterSelect v-model="status" :label="t('product.billing.invoices.status')" :options="[{ value: '', label: t('product.billing.invoices.all') }, ...statuses.map((s) => ({ value: s, label: t(`product.invoiceStatus.${s}`) }))]" />
      <DateRangePicker v-model="range" />
    </DataToolbar>

    <ErrorState :error="invoices.error.value" :retrying="invoices.isFetching.value" compact @retry="invoices.refetch()" />
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
            <TableCell><CustomerLabel :id="inv.customer_external_id" :tenant-id="tenantId" /></TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-md:hidden">{{ inv.period_id }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-md:hidden">{{ day(inv.create_time) }}</TableCell>
            <TableCell><StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill></TableCell>
            <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
            <TableCell><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: ArrowUpRight, onSelect: () => (shown = i) }]" :label="`${t('common.moreActions')} ${inv.number ?? ''}`" /></TableCell>
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
</template>
