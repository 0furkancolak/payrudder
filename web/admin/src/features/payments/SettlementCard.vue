<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import PeekInvoice from "@/features/related/PeekInvoice.vue";
import { useTenant } from "@/features/tenants/api";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { amount, amountHigh, amountLow, newest, oldest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { SortSelect } from "@/shared/ui/sort-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { ym } from "@/shared/lib/dates";
import { useImportSettlement, useSettlementPayments } from "./api";
import { environmentOf, recentPeriods } from "./reconcile";

/**
 * "Paid" is the ledger's word and "settled" the provider's: a payment that succeeded is not money in the bank until the provider paid it out. This lists a period's
 * successful payments with what the provider (or an imported report) says about each, and takes a report as a CSV (docs/reconciliation.md#settlement-csv).
 */
const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const tenant = useTenant(() => props.tenantId);
const environment = computed(() => environmentOf(tenant.data.value?.environment));
const periods = recentPeriods(new Date());
const period = ref(ym(new Date()));
const payments = useSettlementPayments(() => props.tenantId, period);
type Row = NonNullable<NonNullable<typeof payments.data.value>["payments"]>[number];
const rows = computed(() => payments.data.value?.payments ?? []);
const states = ["not_reported", "pending_payout", "settled"] as const;
const tone = (s: string) => (s === "settled" ? "success" : s === "pending_payout" ? "warning" : "neutral");
const money = (minor: number | undefined, currency: string | undefined) => (minor === undefined ? "" : formatMinor(String(minor), currency, locale.value));
const when = (v?: string) => fmt.date(v, "short");

const all = computed(() => ({ value: "", label: t("common.all") }));
const table = useTableState<Row>(rows, {
  url: "settlement-", searchFields: (p) => [p.number, p.invoice_id, p.provider, p.provider_ref, p.settlement_batch],
  sorts: () => [newest(t, (p) => time(p.paid_at)), oldest(t, (p) => time(p.paid_at)), amountHigh(t, (p) => amount(p.amount_minor)), amountLow(t, (p) => amount(p.amount_minor))],
  filters: { state: (p, v) => p.state === v },
});
const stateFilter = computed({ get: () => table.filters.value.state ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, state: v }) });
const stateOptions = computed(() => [all.value, ...states.map((s) => ({ value: s, label: label(t, te, "settlementState", s) }))]);
const counts = computed(() => Object.fromEntries(states.map((s) => [s, rows.value.filter((p) => p.state === s).length])));
const periodOptions = computed(() => periods.map((p) => ({ value: p, label: p })));
const periodDraft = computed({ get: () => period.value, set: (v: string) => (period.value = v || ym(new Date())) });

const importer = useImportSettlement(() => props.tenantId);
const provider = ref("");
const picked = ref<HTMLInputElement | null>(null);
async function importFile() {
  const file = picked.value?.files?.[0];
  if (!file || !environment.value) return;
  await importer.mutateAsync({ csv: await file.text(), environment: environment.value, provider: provider.value.trim() }).catch(() => undefined);
  if (picked.value) picked.value.value = "";
}
</script>

<template>
  <Card class="mb-6" data-testid="settlement-card">
    <div>
      <h2 class="text-lg font-semibold">{{ t("billing.settlement.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.settlement.hint") }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-2" role="form" :aria-label="t('billing.settlement.import')" @submit.prevent="importFile">
      <FormField :label="t('billing.settlement.provider')" for="st-provider" :hint="t('billing.settlement.providerHint')"><Input id="st-provider" v-model="provider" class="w-44" placeholder="stripe" /></FormField>
      <FormField :label="t('billing.settlement.file')" for="st-file"><input id="st-file" ref="picked" type="file" accept=".csv,text/csv" class="field h-10 rounded-xl px-3 text-sm" required /></FormField>
      <Button type="submit" :disabled="importer.isPending.value || !environment">{{ t("billing.settlement.import") }}</Button>
    </form>
    <ErrorText :error="importer.error.value" />
    <p v-if="importer.data.value" class="text-sm" role="status" data-testid="import-result">{{ t("billing.settlement.imported", { lines: importer.data.value.lines, changed: importer.data.value.changed }) }}</p>
    <ul class="flex flex-wrap gap-2" :aria-label="t('billing.settlement.counts')">
      <li v-for="s in states" :key="s"><Badge variant="secondary">{{ label(t, te, "settlementState", s) }}: {{ counts[s] ?? 0 }}</Badge></li>
    </ul>
    <ErrorText :error="payments.error.value" />
    <div class="-mx-3 border-t pt-3">
      <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
        <FilterSelect v-model="periodDraft" :label="t('billing.reconcile.period')" :options="periodOptions" />
        <FilterSelect v-model="stateFilter" multiple :label="t('billing.payments.status')" :options="stateOptions" />
        <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      </DataToolbar>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.payments.invoice") }}</TableHead>
            <TableHead>{{ t("billing.payments.provider") }}</TableHead>
            <TableHead>{{ t("billing.settlement.paidOn") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.payments.amount") }}</TableHead>
            <TableHead>{{ t("billing.payments.status") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.settlement.fee") }}</TableHead>
            <TableHead>{{ t("billing.settlement.batch") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in table.items.value" :key="p.provider + p.provider_ref + p.invoice_id">
            <TableCell><PeekInvoice v-if="p.invoice_id" :id="p.invoice_id" :tenant-id="tenantId" class="mono font-medium hover:underline">{{ p.number || p.invoice_id.slice(0, 8) }}</PeekInvoice></TableCell>
            <TableCell>{{ label(t, te, "provider", p.provider) }} <span class="mono text-xs">{{ p.provider_ref }}</span></TableCell>
            <TableCell class="whitespace-nowrap">{{ when(p.paid_at) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ money(p.amount_minor, p.currency) }}</TableCell>
            <TableCell>
              <StatusPill :tone="tone(p.state)">{{ label(t, te, "settlementState", p.state) }}</StatusPill>
              <span v-if="p.settled_at" class="text-muted-foreground block text-xs">{{ when(p.settled_at) }}</span>
            </TableCell>
            <TableCell class="text-end tabular-nums">
              {{ money(p.fee_minor, p.settlement_currency) }}
              <span v-if="p.expected_fee_minor !== undefined" class="text-muted-foreground block text-xs">{{ t("billing.settlement.expected", { fee: money(p.expected_fee_minor, p.settlement_currency) }) }}</span>
            </TableCell>
            <TableCell class="mono text-xs">{{ p.settlement_batch }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableEmpty v-if="!payments.isPending.value && rows.length === 0 && !payments.error.value">{{ t("billing.settlement.empty") }}</TableEmpty>
      <TableEmpty v-else-if="table.total.value === 0 && table.active.value">{{ t("common.noMatches") }}</TableEmpty>
      <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
    </div>
  </Card>
</template>
