<script setup lang="ts">
import { ArrowUpRight } from "@lucide/vue";
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { ym } from "@/shared/lib/dates";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { useTableState } from "@/shared/lib/useTableState";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import CreditNotesCard from "@/features/creditnotes/CreditNotesCard.vue";
import { PaymentContext, recordLink } from "@/features/related";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { useLedger, useReconcile } from "./api";

const { t, te, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const ledger = useLedger(tenantId);
const reconcile = useReconcile(tenantId);

const now = new Date();
const period = ref(ym(now));
const result = computed(() => reconcile.data.value);
const kinds = computed(() => Object.entries(result.value?.discrepancy_counts ?? {}));
const when = (v?: string) => fmt.date(v, "short");
const all = computed(() => ({ value: "", label: t("common.all") }));
const distinct = (pick: (e: Entry) => string | undefined) => [...new Set((ledger.data.value?.entries ?? []).map((e) => pick(e) ?? "").filter(Boolean))];

// --- the ledger
type Entry = NonNullable<NonNullable<typeof ledger.data.value>["entries"]>[number];
const range = ref<DateRange>(thisMonth());
const table = useTableState<Entry>(() => (ledger.data.value?.entries ?? []).filter((e) => inRange(e.occurred_at, range.value)), {
  searchFields: (e) => [e.provider, e.provider_ref, e.kind, e.source],
  url: true, filters: { status: (e, v) => e.status === v, provider: (e, v) => e.provider === v },
});
const statusFilter = computed({ get: () => table.filters.value.status ?? [], set: (v: string[]) => (table.filters.value.status = v) });
const providerFilter = computed({ get: () => table.filters.value.provider ?? [], set: (v: string[]) => (table.filters.value.provider = v) });
const statuses = computed(() => [all.value, ...distinct((e) => e.status).map((s) => ({ value: s, label: label(t, te, "ledgerStatus", s) }))]);
const providers = computed(() => [all.value, ...distinct((e) => e.provider).map((p) => ({ value: p, label: p }))]);
const ledgerActive = computed(() => table.active.value || range.value.from !== "" || range.value.to !== "");
function clearLedger() {
  table.clear();
  range.value = { from: "", to: "" };
}

const current = table.row;
const detailRows = computed(() => {
  const e = current.value;
  if (!e) return [];
  return [
    { key: "when", label: t("billing.payments.when"), value: when(e.occurred_at) },
    { key: "kind", label: t("billing.payments.kind"), value: label(t, te, "ledgerKind", e.kind) },
    { key: "status", label: t("billing.payments.status") },
    { key: "provider", label: t("billing.payments.provider"), value: e.provider },
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

// --- the discrepancies a reconciliation found
type Discrepancy = NonNullable<NonNullable<typeof reconcile.data.value>["discrepancies"]>[number];
const issues = useTableState<Discrepancy>(() => result.value?.discrepancies ?? [], {
  searchFields: (d) => [d.number, d.invoice_id, d.provider, d.provider_ref, d.detail],
  filters: { kind: (d, v) => d.kind === v },
});
const issueKind = computed({ get: () => issues.filters.value.kind ?? [], set: (v: string[]) => (issues.filters.value.kind = v) });
const issueKinds = computed(() => [all.value, ...kinds.value.map(([k]) => ({ value: k, label: label(t, te, "discrepancy", k) }))]);
</script>

<template>
  <Card class="mb-6 p-3">
    <ErrorState :error="ledger.error.value" :retrying="ledger.isFetching.value" compact @retry="ledger.refetch()" />
    <DataToolbar v-model:search="table.search.value" :active="ledgerActive" @clear="clearLedger">
      <FilterSelect v-model="statusFilter" multiple :label="t('billing.payments.status')" :options="statuses" />
      <FilterSelect v-model="providerFilter" multiple :label="t('billing.payments.provider')" :options="providers" />
      <DateRangePicker v-model="range" />
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
          <TableCell>{{ e.provider }}</TableCell>
          <TableCell>{{ label(t, te, "ledgerSource", e.source) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(String(e.amount_minor ?? 0), e.currency, locale) }}</TableCell>
          <TableCell><RowActions :actions="rowActions(i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!ledger.isPending.value && (ledger.data.value?.entries ?? []).length === 0 && !ledger.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.payments.empty") }}
    </p>
    <p v-else-if="table.total.value === 0 && ledgerActive" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.provider_ref || String(current?.id ?? '') || ''" :description="current ? when(current.occurred_at) : ''">
    <DetailList :rows="detailRows">
      <template #status><StatusPill :tone="toneOf(current?.status)">{{ label(t, te, "ledgerStatus", current?.status) }}</StatusPill></template>
    </DetailList>
    <PaymentContext v-if="current?.invoice_id" :key="current.id" :tenant-id="tenantId" :invoice-id="current.invoice_id" class="mt-6" />
  </DetailSheet>

  <Card class="mb-6">
    <div>
      <h2 class="text-lg font-semibold">{{ t("billing.payments.reconcile") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.payments.reconcileHint") }}</p>
    </div>
    <form class="flex flex-wrap items-center gap-2" role="form" :aria-label="t('billing.payments.reconcile')" @submit.prevent="reconcile.mutate(period)">
      <Input v-model="period" class="w-40" :aria-label="t('billing.payments.period')" placeholder="2026-10" pattern="\d{4}-(0[1-9]|1[0-2])" required />
      <Button type="submit" :disabled="reconcile.isPending.value">{{ t("billing.payments.run") }}</Button>
    </form>
    <ErrorText :error="reconcile.error.value" />
    <div v-if="result" class="grid gap-3" role="region" :aria-label="t('billing.payments.reconcileResult')">
      <p class="text-sm">{{ t("billing.payments.summary", { paid: result.paid_invoices ?? 0, charges: result.successful_charges ?? 0 }) }}</p>
      <p v-if="kinds.length === 0" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status">{{ t("billing.payments.agree") }}</p>
      <ul v-else class="flex flex-wrap gap-2">
        <li v-for="[kind, n] in kinds" :key="kind"><Badge variant="destructive">{{ label(t, te, "discrepancy", kind) }}: {{ n }}</Badge></li>
      </ul>
      <div v-if="(result.discrepancies ?? []).length > 0" class="-mx-3 border-t pt-3">
        <DataToolbar v-model:search="issues.search.value" :active="issues.active.value" @clear="issues.clear()">
          <FilterSelect v-model="issueKind" multiple :label="t('billing.payments.kind')" :options="issueKinds" />
        </DataToolbar>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("billing.payments.invoice") }}</TableHead>
              <TableHead>{{ t("billing.payments.kind") }}</TableHead>
              <TableHead>{{ t("billing.payments.provider") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.payments.billed") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.payments.ledgerAmount") }}</TableHead>
              <TableHead>{{ t("billing.payments.detail") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="(d, i) in issues.items.value" :key="i">
              <TableCell>
                <Link v-if="d.invoice_id" v-bind="recordLink('invoice', d.invoice_id, tenantId)" class="mono font-medium hover:underline">{{ d.number || d.invoice_id.slice(0, 8) }}</Link>
              </TableCell>
              <TableCell>{{ label(t, te, "discrepancy", d.kind) }}</TableCell>
              <TableCell>{{ d.provider }} <span class="mono text-xs">{{ d.provider_ref }}</span></TableCell>
              <TableCell class="text-end tabular-nums">{{ d.billed_minor ? formatMinor(String(d.billed_minor), d.currency, locale) : "" }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ d.ledger_minor ? formatMinor(String(d.ledger_minor), d.currency, locale) : "" }}</TableCell>
              <TableCell class="text-muted-foreground">{{ d.detail }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p v-if="issues.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
        <DataPagination v-model:page="issues.page.value" v-model:size="issues.size.value" :pages="issues.pages.value" :total="issues.total.value" />
      </div>
      <p v-if="result.truncated" class="text-muted-foreground text-xs">{{ t("billing.payments.truncated") }}</p>
    </div>
  </Card>

  <CreditNotesCard :tenant-id="tenantId" />
</template>
