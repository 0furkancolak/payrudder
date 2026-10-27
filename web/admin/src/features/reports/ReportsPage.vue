<script setup lang="ts">
import { thisMonth } from "@/shared/lib/dateRange";
import { Check, ArrowUpRight } from "@lucide/vue";
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { recordLink } from "@/features/related/link";
import { useTableState } from "@/shared/lib/useTableState";
import { ErrorText } from "@/shared/ui/error-text";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { StatusPill } from "@/shared/ui/status-pill";
import { cn } from "@/shared/lib/utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDunningReport, useRevenueReport, useSubscriptionReport } from "./api";

const { t, te } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tabs = ["revenue", "subscriptions", "dunning"] as const;
const tab = ref<(typeof tabs)[number]>("revenue");

// Revenue
// The date range chip drives the period: the report counts whole months, so the days are cut to their month.
const range = ref<DateRange>(thisMonth());
const from = computed(() => range.value.from.slice(0, 7));
const to = computed(() => range.value.to.slice(0, 7));
const split = ref<string[]>(["period"]);
const revenue = useRevenueReport(tenantId, from, to, split);
const toggle = (g: string) => (split.value = split.value.includes(g) ? split.value.filter((x) => x !== g) : [...split.value, g]);
type RevenueRow = NonNullable<NonNullable<typeof revenue.data.value>["rows"]>[number];
const revenueTable = useTableState<RevenueRow>(() => revenue.data.value?.rows ?? [], {
  searchFields: (r) => [r.period, r.provider, r.country, r.currency],
  filters: { currency: (r, v) => r.currency === v },
});
const revenueCurrency = computed({ get: () => revenueTable.filters.value.currency ?? [], set: (v: string[]) => (revenueTable.filters.value.currency = v) });
const revenueCurrencies = computed(() => distinct((revenue.data.value?.rows ?? []).map((r) => r.currency)));
const revenueActive = computed(() => revenueTable.active.value || range.value.from !== "" || range.value.to !== "");
function clearRevenue() {
  revenueTable.clear();
  range.value = { from: "", to: "" };
}
const money = (v: string | number | undefined, currency?: string) => fmt.amount(v, currency);

// Subscriptions
const month = ref("");
const all = computed(() => ({ value: "", label: t("common.all") }));
const distinct = (values: (string | undefined)[]) => [all.value, ...[...new Set(values.map((v) => v ?? "").filter(Boolean))].map((v) => ({ value: v, label: v }))];
/** The last twelve months, newest first, for the month the subscription report is counted at; empty is the current month. */
const months = computed(() => {
  const now = new Date();
  return [{ value: "", label: t("billing.reports.currentMonth") }, ...Array.from({ length: 12 }, (_, i) => {
    const d = new Date(now.getFullYear(), now.getMonth() - i, 1);
    const v = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
    return { value: v, label: v };
  })];
});
const chip = (on: boolean) => cn("inline-flex h-10 items-center gap-1.5 rounded-xl border px-3.5 text-sm font-medium transition-colors", on ? "bg-brand-soft text-brand-strong border-transparent" : "bg-card text-foreground border-border/70 hover:bg-muted");
const subSplit = ref<string[]>([]);
const subs = useSubscriptionReport(tenantId, month, subSplit);
type SubRow = NonNullable<NonNullable<typeof subs.data.value>["rows"]>[number];
const subTable = useTableState<SubRow>(() => subs.data.value?.rows ?? [], {
  searchFields: (r) => [r.provider, r.country, r.currency],
  filters: { currency: (r, v) => r.currency === v },
});
const subCurrency = computed({ get: () => subTable.filters.value.currency ?? [], set: (v: string[]) => (subTable.filters.value.currency = v) });
const subCurrencies = computed(() => distinct((subs.data.value?.rows ?? []).map((r) => r.currency)));
const subActive = computed(() => subTable.active.value || month.value !== "");
function clearSubs() {
  subTable.clear();
  month.value = "";
}
const toggleSub = (g: string) => (subSplit.value = subSplit.value.includes(g) ? subSplit.value.filter((x) => x !== g) : [...subSplit.value, g]);

// Dunning
const dunning = useDunningReport(tenantId);
type DunningRow = NonNullable<NonNullable<typeof dunning.data.value>["invoices"]>[number];
const dunningTable = useTableState<DunningRow>(() => dunning.data.value?.invoices ?? [], {
  searchFields: (d) => [d.number, d.invoice_id, d.customer_external_id, d.provider, d.period],
  filters: { state: (d, v) => d.state === v, currency: (d, v) => d.currency === v },
});
const dunningState = computed({ get: () => dunningTable.filters.value.state ?? [], set: (v: string[]) => (dunningTable.filters.value.state = v) });
const dunningCurrency = computed({ get: () => dunningTable.filters.value.currency ?? [], set: (v: string[]) => (dunningTable.filters.value.currency = v) });
const dunningStates = computed(() => [all.value, ...[...new Set((dunning.data.value?.invoices ?? []).map((d) => d.state ?? "").filter(Boolean))].map((x) => ({ value: x, label: label(t, te, "dunningState", x) }))]);
const dunningCurrencies = computed(() => distinct((dunning.data.value?.invoices ?? []).map((d) => d.currency)));
const when = (v?: string | null) => fmt.date(v, "short", "—");
const dunningTone = (state?: string) => (state === "UNCOLLECTIBLE" ? "danger" : "warning");
const current = dunningTable.row;
const detailRows = computed(() => {
  const d = current.value;
  if (!d) return [];
  return [
    { key: "invoice", label: t("billing.reports.invoice") },
    { key: "customer", label: t("billing.reports.customer"), value: d.customer_external_id },
    { key: "period", label: t("billing.reports.period"), value: d.period },
    { key: "provider", label: t("billing.reports.provider"), value: d.provider },
    { key: "total", label: t("billing.reports.total"), value: money(d.total_minor, d.currency) },
    { key: "state", label: t("billing.reports.state") },
    { key: "attempt", label: t("billing.reports.attempt"), value: d.dunning_attempt },
    { key: "next", label: t("billing.reports.nextAttempt"), value: when(d.next_attempt_time) },
  ];
});
const rowActions = (i: number) => [{ key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => dunningTable.open(i) }];
</script>

<template>
  <div class="mb-4 flex flex-wrap gap-2" role="tablist" :aria-label="t('billing.reports.kinds')">
    <button
      v-for="k in tabs"
      :key="k"
      type="button"
      role="tab"
      :aria-selected="tab === k"
      :class="['inline-flex h-10 items-center rounded-xl border px-4 text-sm font-semibold transition-colors', tab === k ? 'bg-primary text-primary-foreground border-primary shadow-sm' : 'bg-card text-muted-foreground hover:text-foreground border-border/70']"
      @click="tab = k"
    >
      {{ t(`billing.reports.${k}`) }}
    </button>
  </div>

  <Card v-if="tab === 'revenue'" class="p-3">
    <DataToolbar v-model:search="revenueTable.search.value" :active="revenueActive" @clear="clearRevenue">
      <FilterSelect v-model="revenueCurrency" multiple :label="t('billing.reports.currency')" :options="revenueCurrencies" />
      <DateRangePicker v-model="range" />
      <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.reports.splitBy')">
        <button v-for="g in ['period', 'provider', 'country']" :key="g" type="button" role="checkbox" :aria-checked="split.includes(g)" :aria-label="t(`billing.reports.by.${g}`)" :class="chip(split.includes(g))" @click="toggle(g)">
          <Check v-if="split.includes(g)" class="size-4" aria-hidden="true" />{{ t(`billing.reports.by.${g}`) }}
        </button>
      </div>
    </DataToolbar>
    <ErrorText :error="revenue.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead v-if="split.includes('period')">{{ t("billing.reports.by.period") }}</TableHead>
          <TableHead v-if="split.includes('provider')">{{ t("billing.reports.by.provider") }}</TableHead>
          <TableHead v-if="split.includes('country')">{{ t("billing.reports.by.country") }}</TableHead>
          <TableHead>{{ t("billing.reports.currency") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.invoices") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.billed") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.collected") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.refunded") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.outstanding") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.uncollectible") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.credited") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in revenueTable.items.value" :key="i">
          <TableCell v-if="split.includes('period')">{{ r.period }}</TableCell>
          <TableCell v-if="split.includes('provider')">{{ r.provider || "—" }}</TableCell>
          <TableCell v-if="split.includes('country')">{{ r.country || "—" }}</TableCell>
          <TableCell class="font-medium">{{ r.currency }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.invoices }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.billed_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.collected_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.refunded_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.outstanding_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.uncollectible_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(r.credited_minor, r.currency) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!revenue.isPending.value && (revenue.data.value?.rows ?? []).length === 0 && !revenue.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.reports.empty") }}</p>
    <p v-else-if="revenueTable.total.value === 0 && revenueActive" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="revenueTable.page.value" v-model:size="revenueTable.size.value" :pages="revenueTable.pages.value" :total="revenueTable.total.value" />
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.revenueHint") }}</p>
  </Card>

  <Card v-else-if="tab === 'subscriptions'" class="p-3">
    <DataToolbar v-model:search="subTable.search.value" :active="subActive" @clear="clearSubs">
      <FilterSelect v-model="month" :label="t('billing.reports.month')" :options="months" />
      <FilterSelect v-model="subCurrency" multiple :label="t('billing.reports.currency')" :options="subCurrencies" />
      <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.reports.splitBy')">
        <button v-for="g in ['provider', 'country']" :key="g" type="button" role="checkbox" :aria-checked="subSplit.includes(g)" :aria-label="t(`billing.reports.by.${g}`)" :class="chip(subSplit.includes(g))" @click="toggleSub(g)">
          <Check v-if="subSplit.includes(g)" class="size-4" aria-hidden="true" />{{ t(`billing.reports.by.${g}`) }}
        </button>
      </div>
    </DataToolbar>
    <ErrorText :error="subs.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead v-if="subSplit.includes('provider')">{{ t("billing.reports.by.provider") }}</TableHead>
          <TableHead v-if="subSplit.includes('country')">{{ t("billing.reports.by.country") }}</TableHead>
          <TableHead>{{ t("billing.reports.currency") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.active") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.trialing") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.paused") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.pastDue") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.mrr") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.new") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.churned") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in subTable.items.value" :key="i">
          <TableCell v-if="subSplit.includes('provider')">{{ r.provider || "—" }}</TableCell>
          <TableCell v-if="subSplit.includes('country')">{{ r.country || "—" }}</TableCell>
          <TableCell class="font-medium">{{ r.currency }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.active }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.trialing }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.paused }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.past_due }}</TableCell>
          <TableCell class="text-end font-medium tabular-nums">{{ money(r.mrr_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.new_subscriptions }} · {{ money(r.new_mrr_minor, r.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.churned }} · {{ money(r.churned_mrr_minor, r.currency) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!subs.isPending.value && (subs.data.value?.rows ?? []).length === 0 && !subs.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.reports.empty") }}</p>
    <p v-else-if="subTable.total.value === 0 && subActive" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="subTable.page.value" v-model:size="subTable.size.value" :pages="subTable.pages.value" :total="subTable.total.value" />
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.subscriptionsHint") }}</p>
  </Card>

  <Card v-else class="p-3">
    <ul v-if="(dunning.data.value?.summary ?? []).length > 0" class="flex flex-wrap gap-2 px-3 pb-3">
      <li v-for="(s, i) in dunning.data.value?.summary ?? []" :key="i">
        <StatusPill :tone="dunningTone(s.state)">{{ label(t, te, "dunningState", s.state) }} · {{ s.provider || "—" }} · {{ s.currency }}: {{ s.invoices }} · {{ money(s.total_minor, s.currency) }}</StatusPill>
      </li>
    </ul>
    <ErrorText :error="dunning.error.value" class="px-3" />
    <DataToolbar v-model:search="dunningTable.search.value" :active="dunningTable.active.value" @clear="dunningTable.clear()">
      <FilterSelect v-model="dunningState" multiple :label="t('billing.reports.state')" :options="dunningStates" />
      <FilterSelect v-model="dunningCurrency" multiple :label="t('billing.reports.currency')" :options="dunningCurrencies" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.reports.invoice") }}</TableHead>
          <TableHead>{{ t("billing.reports.customer") }}</TableHead>
          <TableHead>{{ t("billing.reports.period") }}</TableHead>
          <TableHead>{{ t("billing.reports.provider") }}</TableHead>
          <TableHead>{{ t("billing.reports.state") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.total") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.attempt") }}</TableHead>
          <TableHead>{{ t("billing.reports.nextAttempt") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(d, i) in dunningTable.items.value" :key="d.invoice_id" class="cursor-pointer" @click="dunningTable.open(i)">
          <TableCell><Link v-bind="recordLink('invoice', d.invoice_id ?? '', tenantId)" class="font-medium hover:underline" @click.stop>{{ d.number || d.invoice_id?.slice(0, 8) }}</Link></TableCell>
          <TableCell>{{ d.customer_external_id }}</TableCell>
          <TableCell>{{ d.period }}</TableCell>
          <TableCell>{{ d.provider || "—" }}</TableCell>
          <TableCell><StatusPill :tone="dunningTone(d.state)">{{ label(t, te, "dunningState", d.state) }}</StatusPill></TableCell>
          <TableCell class="text-end tabular-nums">{{ money(d.total_minor, d.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ d.dunning_attempt }}</TableCell>
          <TableCell class="whitespace-nowrap">{{ when(d.next_attempt_time) }}</TableCell>
          <TableCell><RowActions :actions="rowActions(i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!dunning.isPending.value && (dunning.data.value?.invoices ?? []).length === 0 && !dunning.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.reports.noDunning") }}</p>
    <p v-else-if="dunningTable.total.value === 0 && dunningTable.active.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="dunningTable.page.value" v-model:size="dunningTable.size.value" :pages="dunningTable.pages.value" :total="dunningTable.total.value" />
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.dunningHint") }}</p>
  </Card>

  <DetailSheet v-model:index="dunningTable.detail.value" :total="dunningTable.total.value" :title="current?.number || current?.invoice_id?.slice(0, 8) || ''" :description="t('billing.reports.dunning')">
    <DetailList :rows="detailRows">
      <template #invoice><Link v-bind="recordLink('invoice', current?.invoice_id ?? '', tenantId)" class="font-medium hover:underline">{{ current?.number || current?.invoice_id?.slice(0, 8) }}</Link></template>
      <template #state><StatusPill :tone="dunningTone(current?.state)">{{ label(t, te, "dunningState", current?.state) }}</StatusPill></template>
    </DetailList>
  </DetailSheet>
</template>
