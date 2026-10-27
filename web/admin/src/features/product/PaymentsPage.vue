<script setup lang="ts">
import { useNavigate, useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorState } from "@/shared/ui/error-state";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Card } from "@/shared/ui/card";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { ArrowUpRight, FileText } from "@lucide/vue";
import { useTableState } from "@/shared/lib/useTableState";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { PaymentContext } from "@/features/related";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { usePayments } from "./billingApi";

/**
 * The environment's payments, newest first: charges and refunds, each as the one outcome it settled on (pending, paid, failed, refunded). Reads /v1/payments.
 * The operator's ledger (provider references, decline details) and the reconciliation are operator tools and are not here.
 */
const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const payments = usePayments(tenantId);
const all = computed(() => payments.data.value?.payments ?? []);
const range = ref<DateRange>(thisMonth());
const rows = computed(() => all.value.filter((p) => inRange(p.occurred_at, range.value)));
const navigate = useNavigate();
const toInvoice = (id?: string) => void navigate({ to: "/app/t/$id/invoices", params: { id: tenantId.value }, search: { invoice: id ?? "" } as never });
const table = useTableState(rows, {
  sheet: { kind: "payment", id: (p) => p.id }, searchFields: (p) => [p.provider, p.invoice_id, p.id, p.amount_minor],
  url: true, filters: { status: (p, v) => p.status === v, kind: (p, v) => p.kind === v },
});
const statuses = computed(() => [...new Set(all.value.map((p) => p.status as string | undefined).filter((v): v is string => !!v))].sort());
const kinds = computed(() => [...new Set(all.value.map((p) => p.kind as string | undefined).filter((v): v is string => !!v))].sort());
const filtering = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clear() {
  table.clear();
  range.value = { from: "", to: "" };
}
const current = table.row;
watch([table.search, table.filters, table.size, range], () => (table.detail.value = null), { deep: true });
// A link from an invoice or a customer opens the payment it names; a filter that hides it is cleared first.
useOpenParam("payment", () => payments.isSuccess.value, (id) => {
  if (!table.openWhere((p) => p.id === id)) {
    clear();
    table.openWhere((p) => p.id === id);
  }
});
const signed = (p: NonNullable<typeof current.value>) => formatMinor(p.status === "refunded" ? `-${p.amount_minor}` : p.amount_minor, p.currency, locale.value);
const when = (v?: string) => fmt.date(v, "dateTime");
</script>

<template>
  <Card class="p-3" role="region" :aria-label="t('product.billing.payments.title')">
    <ErrorState :error="payments.error.value" :retrying="payments.isFetching.value" compact @retry="payments.refetch()" />
    <DataToolbar v-if="all.length > 0" v-model:search="table.search.value" :active="filtering" @clear="clear()">
      <FilterSelect v-model="table.filters.value.status" multiple :label="t('product.payments.status')" :options="[{ value: '', label: t('product.payments.allStatuses') }, ...statuses.map((s) => ({ value: s, label: t(`product.status.${s}`) }))]" />
      <FilterSelect v-model="table.filters.value.kind" multiple :label="t('product.billing.payments.kind')" :options="[{ value: '', label: t('product.payments.allKinds') }, ...kinds.map((k) => ({ value: k, label: t(`product.billing.payments.kinds.${k}`) }))]" />
      <DateRangePicker v-model="range" />
      <template #actions>
        <span v-if="payments.data.value?.truncated" class="text-muted-foreground text-xs">{{ t("product.payments.truncated") }}</span>
      </template>
    </DataToolbar>
    <Skeleton v-if="payments.isPending.value" class="mx-3 h-40 rounded-2xl" />
    <p v-else-if="all.length === 0 && !payments.error.value" class="text-muted-foreground py-10 text-center text-sm">{{ t("product.billing.payments.empty") }}</p>
    <template v-else-if="all.length > 0">
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("product.payments.when") }}</TableHead>
            <TableHead>{{ t("product.billing.payments.kind") }}</TableHead>
            <TableHead>{{ t("product.payments.status") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("product.payments.provider") }}</TableHead>
            <TableHead class="text-end">{{ t("product.payments.amount") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(p, i) in table.items.value" :key="p.id" class="cursor-pointer" data-testid="payment-row" @click="table.open(i)">
            <TableCell class="font-medium whitespace-nowrap">{{ when(p.occurred_at) }}</TableCell>
            <TableCell>{{ t(`product.billing.payments.kinds.${p.kind}`) }}</TableCell>
            <TableCell><StatusPill :tone="toneOf(p.status)">{{ t(`product.status.${p.status}`) }}</StatusPill></TableCell>
            <TableCell class="text-muted-foreground capitalize max-md:hidden">{{ p.provider }}</TableCell>
            <TableCell class="text-end font-medium whitespace-nowrap tabular">{{ signed(p) }}</TableCell>
            <TableCell>
              <RowActions
                :actions="[
                  { key: 'view', label: t('common.view'), icon: ArrowUpRight, onSelect: () => table.open(i) },
                  ...(p.invoice_id ? [{ key: 'invoice', label: t('product.billing.payments.viewInvoice'), icon: FileText, onSelect: () => toInvoice(p.invoice_id) }] : []),
                ]"
              />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="table.total.value === 0" class="text-muted-foreground py-10 text-center text-sm">{{ t("common.noMatches") }}</p>
    </template>
    <DataPagination v-if="all.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current ? signed(current) : ''" :description="current ? when(current.occurred_at) : undefined">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm" data-testid="payment-detail">
      <dt class="text-muted-foreground">{{ t("product.billing.payments.kind") }}</dt>
      <dd>{{ t(`product.billing.payments.kinds.${current.kind}`) }}</dd>
      <dt class="text-muted-foreground">{{ t("product.payments.status") }}</dt>
      <dd><StatusPill :tone="toneOf(current.status)">{{ t(`product.status.${current.status}`) }}</StatusPill></dd>
      <dt class="text-muted-foreground">{{ t("product.payments.provider") }}</dt>
      <dd class="capitalize">{{ current.provider }}</dd>
      <dt class="text-muted-foreground">{{ t("product.payments.amount") }}</dt>
      <dd class="tabular font-medium">{{ signed(current) }}</dd>
    </dl>
    <PaymentContext v-if="current?.invoice_id" :key="current.id" :tenant-id="tenantId" :invoice-id="current.invoice_id" class="mt-6" />
  </DetailSheet>
</template>
