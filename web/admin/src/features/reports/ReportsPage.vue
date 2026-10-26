<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { tenantBase } from "@/shared/lib/session";
import { useTableState } from "@/shared/lib/useTableState";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDunningReport, useRevenueReport, useSubscriptionReport } from "./api";

const { t, te } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tabs = ["revenue", "subscriptions", "dunning"] as const;
const tab = ref<(typeof tabs)[number]>("revenue");

// Revenue
const from = ref("");
const to = ref("");
const split = ref<string[]>(["period"]);
const revenue = useRevenueReport(tenantId, from, to, split);
const toggle = (g: string) => (split.value = split.value.includes(g) ? split.value.filter((x) => x !== g) : [...split.value, g]);
type RevenueRow = NonNullable<NonNullable<typeof revenue.data.value>["rows"]>[number];
const revenueTable = useTableState<RevenueRow>(() => revenue.data.value?.rows ?? [], {
  searchFields: (r) => [r.period, r.provider, r.country, r.currency],
  filters: { currency: (r, v) => r.currency === v },
});
const revenueCurrency = computed({ get: () => revenueTable.filters.value.currency ?? "", set: (v) => (revenueTable.filters.value.currency = String(v)) });
const revenueCurrencies = computed(() => [...new Set((revenue.data.value?.rows ?? []).map((r) => r.currency ?? "").filter(Boolean))]);
const money = (v: string | number | undefined, currency?: string) => fmt.amount(v, currency);

// Subscriptions
const month = ref("");
const subSplit = ref<string[]>([]);
const subs = useSubscriptionReport(tenantId, month, subSplit);
type SubRow = NonNullable<NonNullable<typeof subs.data.value>["rows"]>[number];
const subTable = useTableState<SubRow>(() => subs.data.value?.rows ?? [], {
  searchFields: (r) => [r.provider, r.country, r.currency],
  filters: { currency: (r, v) => r.currency === v },
});
const subCurrency = computed({ get: () => subTable.filters.value.currency ?? "", set: (v) => (subTable.filters.value.currency = String(v)) });
const subCurrencies = computed(() => [...new Set((subs.data.value?.rows ?? []).map((r) => r.currency ?? "").filter(Boolean))]);
const toggleSub = (g: string) => (subSplit.value = subSplit.value.includes(g) ? subSplit.value.filter((x) => x !== g) : [...subSplit.value, g]);

// Dunning
const dunning = useDunningReport(tenantId);
type DunningRow = NonNullable<NonNullable<typeof dunning.data.value>["invoices"]>[number];
const dunningTable = useTableState<DunningRow>(() => dunning.data.value?.invoices ?? [], {
  searchFields: (d) => [d.number, d.invoice_id, d.customer_external_id, d.provider, d.period],
  filters: { state: (d, v) => d.state === v, currency: (d, v) => d.currency === v },
});
const dunningState = computed({ get: () => dunningTable.filters.value.state ?? "", set: (v) => (dunningTable.filters.value.state = String(v)) });
const dunningCurrency = computed({ get: () => dunningTable.filters.value.currency ?? "", set: (v) => (dunningTable.filters.value.currency = String(v)) });
const dunningStates = computed(() => [...new Set((dunning.data.value?.invoices ?? []).map((d) => d.state ?? "").filter(Boolean))]);
const dunningCurrencies = computed(() => [...new Set((dunning.data.value?.invoices ?? []).map((d) => d.currency ?? "").filter(Boolean))]);
const when = (v?: string) => fmt.date(v, "short", "—");
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
    <div class="flex flex-wrap items-end gap-4 px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.reports.revenue") }}</h2>
      <Input v-model="from" class="w-32" :aria-label="t('billing.reports.from')" :placeholder="t('billing.reports.from')" pattern="\d{4}-(0[1-9]|1[0-2])" />
      <Input v-model="to" class="w-32" :aria-label="t('billing.reports.to')" :placeholder="t('billing.reports.to')" pattern="\d{4}-(0[1-9]|1[0-2])" />
      <label v-for="g in ['period', 'provider', 'country']" :key="g" class="flex items-center gap-2 text-sm">
        <Checkbox :model-value="split.includes(g)" :aria-label="t(`billing.reports.by.${g}`)" @update:model-value="toggle(g)" />{{ t(`billing.reports.by.${g}`) }}
      </label>
    </div>
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.revenueHint") }}</p>
    <ErrorText :error="revenue.error.value" class="px-3" />
    <DataToolbar v-model:search="revenueTable.search.value" :active="revenueTable.active.value" class="pt-3" @clear="revenueTable.clear()">
      <NativeSelect v-model="revenueCurrency" class="w-36" :aria-label="t('billing.reports.currency')">
        <option value="">{{ t("billing.reports.currency") }}</option>
        <option v-for="c in revenueCurrencies" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
    </DataToolbar>
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
    <DataPagination v-model:page="revenueTable.page.value" v-model:size="revenueTable.size.value" :pages="revenueTable.pages.value" :total="revenueTable.total.value" />
  </Card>

  <Card v-else-if="tab === 'subscriptions'" class="p-3">
    <div class="flex flex-wrap items-end gap-4 px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.reports.subscriptions") }}</h2>
      <Input v-model="month" class="w-32" :aria-label="t('billing.reports.month')" :placeholder="t('billing.reports.month')" pattern="\d{4}-(0[1-9]|1[0-2])" />
      <label v-for="g in ['provider', 'country']" :key="g" class="flex items-center gap-2 text-sm">
        <Checkbox :model-value="subSplit.includes(g)" :aria-label="t(`billing.reports.by.${g}`)" @update:model-value="toggleSub(g)" />{{ t(`billing.reports.by.${g}`) }}
      </label>
    </div>
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.subscriptionsHint") }}</p>
    <ErrorText :error="subs.error.value" class="px-3" />
    <DataToolbar v-model:search="subTable.search.value" :active="subTable.active.value" class="pt-3" @clear="subTable.clear()">
      <NativeSelect v-model="subCurrency" class="w-36" :aria-label="t('billing.reports.currency')">
        <option value="">{{ t("billing.reports.currency") }}</option>
        <option v-for="c in subCurrencies" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
    </DataToolbar>
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
    <DataPagination v-model:page="subTable.page.value" v-model:size="subTable.size.value" :pages="subTable.pages.value" :total="subTable.total.value" />
  </Card>

  <Card v-else class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.reports.dunning") }}</h2>
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.dunningHint") }}</p>
    <ErrorText :error="dunning.error.value" class="px-3" />
    <ul class="flex flex-wrap gap-2 px-3">
      <li v-for="(s, i) in dunning.data.value?.summary ?? []" :key="i">
        <Badge variant="secondary">{{ label(t, te, "dunningState", s.state) }} · {{ s.provider || "—" }} · {{ s.currency }}: {{ s.invoices }} · {{ money(s.total_minor, s.currency) }}</Badge>
      </li>
    </ul>
    <DataToolbar v-model:search="dunningTable.search.value" :active="dunningTable.active.value" class="pt-3" @clear="dunningTable.clear()">
      <NativeSelect v-model="dunningState" class="w-44" :aria-label="t('billing.reports.state')">
        <option value="">{{ t("billing.reports.state") }}</option>
        <option v-for="x in dunningStates" :key="x" :value="x">{{ label(t, te, "dunningState", x) }}</option>
      </NativeSelect>
      <NativeSelect v-model="dunningCurrency" class="w-36" :aria-label="t('billing.reports.currency')">
        <option value="">{{ t("billing.reports.currency") }}</option>
        <option v-for="c in dunningCurrencies" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.reports.invoice") }}</TableHead>
          <TableHead>{{ t("billing.reports.customer") }}</TableHead>
          <TableHead>{{ t("billing.reports.period") }}</TableHead>
          <TableHead>{{ t("billing.reports.provider") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.total") }}</TableHead>
          <TableHead>{{ t("billing.reports.state") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.reports.attempt") }}</TableHead>
          <TableHead>{{ t("billing.reports.nextAttempt") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="d in dunningTable.items.value" :key="d.invoice_id">
          <TableCell><Link :to="`${tenantBase()}/$id/invoices/$invoiceId`" :params="{ id: tenantId, invoiceId: d.invoice_id ?? '' }" class="font-medium hover:underline">{{ d.number || d.invoice_id?.slice(0, 8) }}</Link></TableCell>
          <TableCell>{{ d.customer_external_id }}</TableCell>
          <TableCell>{{ d.period }}</TableCell>
          <TableCell>{{ d.provider || "—" }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ money(d.total_minor, d.currency) }}</TableCell>
          <TableCell><Badge :variant="d.state === 'UNCOLLECTIBLE' ? 'destructive' : 'secondary'">{{ label(t, te, "dunningState", d.state) }}</Badge></TableCell>
          <TableCell class="text-end tabular-nums">{{ d.dunning_attempt }}</TableCell>
          <TableCell>{{ when(d.next_attempt_time) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!dunning.isPending.value && (dunning.data.value?.invoices ?? []).length === 0 && !dunning.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.reports.noDunning") }}</p>
    <DataPagination v-model:page="dunningTable.page.value" v-model:size="dunningTable.size.value" :pages="dunningTable.pages.value" :total="dunningTable.total.value" />
  </Card>
</template>
