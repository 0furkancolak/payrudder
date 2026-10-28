<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import CustomerLabel from "@/features/customers/CustomerLabel.vue";
import { Settings2 } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { Subscription } from "@/shared/api/client";
import { inRange, rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { useFormat } from "@/shared/lib/format";
import { enumWord, label } from "@/shared/lib/labels";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { SortSelect } from "@/shared/ui/sort-select";
import { newest, oldest, time } from "@/shared/lib/sortDefs";

import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useSubscriptions } from "./api";
import { intervalKey, seatsOf, TRIAL_WARNING_DAYS, trialDaysLeft, trialsEndingSoon } from "./lib";

/** The tenant's subscriptions. A row opens a sheet (the `detail` slot, which gets the subscription) that steps through the rows the list shows. */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ create: [] }>();
defineSlots<{ detail(p: { subscription: Subscription }): unknown }>();
const { t, te } = useI18n();
const fmt = useFormat();
const subs = useSubscriptions(() => props.tenantId);
const list = computed(() => subs.data.value?.subscriptions ?? []);

const state = (s: Subscription) => enumWord(s.state, "SUBSCRIPTION_STATE_");
const BAD_STATES = ["cancelled", "past_due"];
const stateVariant = (s: Subscription) => (state(s) === "active" ? "success" : BAD_STATES.includes(state(s)) ? "destructive" : "secondary");
const range = ref<DateRange>(thisMonth());
const table = useTableState(() => list.value.filter((s) => inRange(s.start_time, range.value)), {
  sorts: () => [newest(t, (s) => time(s.start_time)), oldest(t, (s) => time(s.start_time))],
  sheet: { kind: "subscription", id: (s) => s.id }, searchFields: (s) => [s.customer_external_id, s.plan_code, s.provider],
  url: true, filters: { state: (s, v) => state(s) === v, currency: (s, v) => s.currency === v, interval: (s, v) => s.interval === v },
});
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || rangeChanged(range.value));
function clear() {
  table.clear();
  range.value = thisMonth();
}
const filter = (key: string) => computed({ get: () => table.filters.value[key] ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const stateFilter = filter("state");
const currencyFilter = filter("currency");
const intervalFilter = filter("interval");
const stateOptions = computed(() => [{ value: "", label: t("billing.subscriptions.allStates") }, ...[...new Set(list.value.map(state))].sort().map((s) => ({ value: s, label: label(t, te, "subscriptionState", s) }))]);
const currencyOptions = computed(() => [{ value: "", label: t("billing.subscriptions.allCurrencies") }, ...[...new Set(list.value.map((s) => s.currency).filter((c): c is string => !!c))].sort().map((c) => ({ value: c, label: c }))]);
const intervalOptions = computed(() => [
  { value: "", label: t("billing.subscriptions.allIntervals") },
  ...[...new Set(list.value.map((s) => s.interval).filter((i): i is NonNullable<Subscription["interval"]> => !!i))].map((i) => ({ value: i, label: t(`billing.subscriptions.${intervalKey(i)}`) })),
]);
const date = (v?: string | null) => fmt.date(v, "day", "—");

// Trials that are about to end: a subscription then starts to be billed, so the person is told ahead (and can open them with one click).
const ending = computed(() => trialsEndingSoon(list.value));
function showTrials() {
  table.filters.value = { ...table.filters.value, state: ["trialing"] };
}

const current = table.row;
// A link from a customer or an invoice opens the subscription it names; a filter that hides it is cleared first.
useOpenParam("subscription", () => subs.isSuccess.value, (id) => {
  if (!table.openWhere((s) => s.id === id)) {
    clear();
    table.openWhere((s) => s.id === id);
  }
});
</script>

<template>
  <Card class="p-3">
    <ErrorText :error="subs.error.value" class="px-3" />
    <div v-if="ending.length > 0" class="bg-warning-soft text-warning mx-3 mb-3 flex flex-wrap items-center justify-between gap-2 rounded-2xl px-4 py-2.5 text-sm" role="status" data-testid="trials-ending">
      <span>{{ t("billing.subscriptions.trialsEnding", { n: ending.length, days: TRIAL_WARNING_DAYS }, ending.length) }}</span>
      <Button type="button" size="sm" variant="outline" data-testid="trials-ending-show" @click="showTrials">{{ t("billing.subscriptions.showTrials") }}</Button>
    </div>
    <DataToolbar v-model:search="table.search.value" :active="active" @clear="clear">
      <FilterSelect v-model="stateFilter" multiple :label="t('billing.subscriptions.state')" :options="stateOptions" />
      <FilterSelect v-model="currencyFilter" multiple :label="t('billing.subscriptions.currency')" :options="currencyOptions" />
      <FilterSelect v-model="intervalFilter" multiple :label="t('billing.subscriptions.interval')" :options="intervalOptions" />
      <DateRangePicker v-model="range" />
      <HeaderAction test-id="subscriptions-new" :label="t('billing.subscriptions.new')" @click="emit('create')" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.subscriptions.customer") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.package") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.subscriptions.seats") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.interval") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.currency") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.provider") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.since") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.until") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(s, i) in table.items.value" :key="s.id" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
          <TableCell class="font-medium"><CustomerLabel :id="s.customer_external_id" :tenant-id="tenantId" /></TableCell>
          <TableCell class="mono">{{ s.plan_code }} v{{ s.plan_version }}</TableCell>
          <TableCell class="text-end tabular-nums" data-testid="sub-seats-cell">{{ seatsOf(s) }}</TableCell>
          <TableCell>
            <Badge :variant="stateVariant(s)">{{ label(t, te, "subscriptionState", state(s)) }}</Badge>
            <span v-if="s.cancel_at_period_end" class="text-muted-foreground ms-2 text-xs">{{ t("billing.subscriptions.endsWithPeriod") }}</span>
            <span v-if="trialDaysLeft(s) !== null" class="text-muted-foreground ms-2 text-xs" data-testid="trial-end">{{ t("billing.subscriptions.trialEnds", { date: date(s.trial_end_time) }) }}</span>
          </TableCell>
          <TableCell>{{ t(`billing.subscriptions.${intervalKey(s.interval)}`) }}</TableCell>
          <TableCell>{{ s.currency }}</TableCell>
          <TableCell>
            {{ label(t, te, "provider", s.provider) || "—" }}
            <Badge v-if="s.owner === 'provider'" variant="secondary" class="ms-1">{{ t("billing.subscriptions.runByProvider") }}</Badge>
          </TableCell>
          <TableCell>{{ date(s.start_time) }}</TableCell>
          <TableCell>{{ date(s.end_time) }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="[{ key: 'manage', label: t('billing.subscriptions.manage'), icon: Settings2, onSelect: () => table.open(i) }]" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <TableEmpty v-if="!subs.isPending.value && list.length === 0 && !subs.error.value">
      {{ t("billing.subscriptions.empty") }}
    </TableEmpty>
    <TableEmpty v-else-if="list.length > 0 && table.total.value === 0">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="t('billing.subscriptions.managing', { customer: current?.customer_external_id ?? '' })" :description="current ? `${current.plan_code} v${current.plan_version}${seatsOf(current) > 1 ? ' · ' + t('billing.subscriptions.seatsCount', { n: seatsOf(current) }, seatsOf(current)) : ''}` : undefined" wide>
    <slot v-if="current" name="detail" :subscription="current" />
  </DetailSheet>
</template>
