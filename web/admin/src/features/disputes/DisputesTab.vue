<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { FileText } from "@lucide/vue";
import { peek } from "@/features/related/peek";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { amount, amountHigh, amountLow, newest, oldest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorState } from "@/shared/ui/error-state";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { SortSelect } from "@/shared/ui/sort-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDisputes } from "./api";
import { disputeKind, disputeTone, OUTCOMES, type Dispute } from "./dispute";

/**
 * The cases customers opened with their banks about the environment's payments, newest first. A client list like the payments beside it (the newest 200, filtered and
 * ordered in the browser): outcome and kind apply on their button, one select orders, a row opens the invoice the payment was for.
 */
const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const disputes = useDisputes(() => props.tenantId);
const all = computed(() => disputes.data.value?.disputes ?? []);
const table = useTableState(all, {
  sorts: () => [newest(t, (d) => time(d.opened_time)), oldest(t, (d) => time(d.opened_time)), amountHigh(t, (d) => amount(d.amount_minor)), amountLow(t, (d) => amount(d.amount_minor))],
  searchFields: (d) => [d.dispute_id, d.invoice_id, d.payment_ref, d.provider, d.reason],
  url: "disputes-", filters: { outcome: (d, v) => d.outcome === v, kind: (d, v) => disputeKind(d) === v, handled: (d, v) => (d.handled ? "yes" : "no") === v },
});
const toInvoice = (d: Dispute) => d.invoice_id && peek("invoice", props.tenantId, d.invoice_id);
const money = (d: Dispute) => formatMinor(d.amount_minor, d.currency, locale.value);
</script>

<template>
  <Card class="p-3" role="region" :aria-label="t('disputes.tab')">
    <ErrorState :error="disputes.error.value" :retrying="disputes.isFetching.value" compact @retry="disputes.refetch()" />
    <DataToolbar v-if="all.length > 0" v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="table.filters.value.outcome" multiple :label="t('disputes.outcomeLabel')" :options="[{ value: '', label: t('disputes.allOutcomes') }, ...OUTCOMES.map((o) => ({ value: o, label: t(`disputes.outcome.${o}`) }))]" />
      <FilterSelect v-model="table.filters.value.kind" multiple :label="t('disputes.kindLabel')" :options="[{ value: '', label: t('disputes.allKinds') }, ...(['chargeback', 'inquiry'] as const).map((k) => ({ value: k, label: t(`disputes.kind.${k}`) }))]" />
      <FilterSelect v-model="table.filters.value.handled" multiple :label="t('disputes.handledLabel')" :options="[{ value: '', label: t('disputes.allHandled') }, { value: 'yes', label: t('disputes.handledYes') }, { value: 'no', label: t('disputes.handledNo') }]" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>
    <Skeleton v-if="disputes.isPending.value" class="mx-3 h-40 rounded-2xl" />
    <p v-else-if="all.length === 0 && !disputes.error.value" class="text-muted-foreground py-10 text-center text-sm" data-testid="disputes-empty">{{ t("disputes.empty") }}</p>
    <template v-else-if="all.length > 0">
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("disputes.opened") }}</TableHead>
            <TableHead>{{ t("disputes.case") }}</TableHead>
            <TableHead>{{ t("disputes.outcomeLabel") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("disputes.provider") }}</TableHead>
            <TableHead class="text-end">{{ t("product.payments.amount") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="d in table.items.value" :key="d.id" :class="d.invoice_id ? 'cursor-pointer' : ''" data-testid="dispute-row" @click="toInvoice(d)">
            <TableCell class="font-medium whitespace-nowrap">{{ fmt.date(d.opened_time, "dateTime") }}</TableCell>
            <TableCell>
              <div class="font-medium">{{ t(`disputes.kind.${disputeKind(d)}`) }}</div>
              <div class="text-muted-foreground mono text-xs">{{ d.dispute_id }}</div>
            </TableCell>
            <TableCell>
              <div class="flex flex-wrap items-center gap-1.5">
                <StatusPill :tone="disputeTone(d.outcome)">{{ t(`disputes.outcome.${d.outcome}`) }}</StatusPill>
                <StatusPill v-if="d.handled" tone="info" data-testid="dispute-handled-pill">{{ t("disputes.handled") }}</StatusPill>
              </div>
            </TableCell>
            <TableCell class="text-muted-foreground max-md:hidden">{{ label(t, te, "provider", d.provider) }}</TableCell>
            <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">{{ money(d) }}</TableCell>
            <TableCell>
              <RowActions v-if="d.invoice_id" :actions="[{ key: 'invoice', label: t('product.billing.payments.viewInvoice'), icon: FileText, onSelect: () => toInvoice(d) }]" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="table.total.value === 0" class="text-muted-foreground py-10 text-center text-sm">{{ t("common.noMatches") }}</p>
    </template>
    <DataPagination v-if="all.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
