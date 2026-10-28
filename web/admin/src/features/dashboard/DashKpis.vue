<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import KpiCard from "./KpiCard.vue";
import { kpiFigures, type DashCurrency, type Dashboard } from "./lib";

/** The eight figures at the top of the dashboard, for the chosen currency, each with its change against the period before. A part that did not answer shows a dash. */
const props = defineProps<{ data: Dashboard; block: DashCurrency; cur: string; compare: boolean; base: string }>();
const { t } = useI18n();
const fmt = useFormat();

const money = (minor: number | null | undefined) => fmt.amount(minor, props.cur);
const count = fmt.count;
const pct = fmt.percent;
const down = (name: string) => props.data.unavailable?.includes(name) ?? false;

const rev = computed(() => props.block.revenue);
const subs = computed(() => props.block.subscriptions);
const pay = computed(() => props.block.payments);
const prevRev = computed(() => (props.compare ? props.block.prev_revenue : null));
const prevSubs = computed(() => (props.compare ? props.block.prev_subscriptions : null));
const k = computed(() => kpiFigures(props.block, props.data.customers, props.compare));
const spark = computed(() => (props.block.series ?? []).map((p) => p.net_minor ?? 0));
const customersDown = computed(() => down("customers") || !props.data.customers);
</script>

<template>
  <div class="flex snap-x snap-mandatory gap-3 overflow-x-auto pb-2 max-sm:-mx-4 max-sm:scroll-px-4 max-sm:px-4 max-sm:[&>*]:w-[46%] max-sm:[&>*]:shrink-0 max-sm:[&>*]:snap-start sm:grid sm:snap-none sm:grid-cols-2 sm:gap-5 sm:overflow-visible sm:pb-0 xl:grid-cols-4">
    <KpiCard testid="revenue" :label="t('dash.kpi.net')" :delta="k.dNet" :previous="prevRev ? money(prevRev.net_minor) : undefined" :spark="spark">
      <template #value>{{ money(rev?.net_minor) }}</template>
      <template #sub>{{ t("dash.kpi.netSub", { gross: money(rev?.gross_minor), refunds: money(rev?.refunds_minor) }) }}</template>
    </KpiCard>
    <KpiCard testid="mrr" :label="t('dash.kpi.mrr')" :delta="k.dMrr" :previous="prevSubs ? money(prevSubs.mrr_minor) : undefined">
      <template #value>{{ down("subscriptions") ? "—" : money(subs?.mrr_minor) }}</template>
      <template #sub>{{ down("subscriptions") ? t("product.unavailable") : t("dash.kpi.mrrSub", { arr: money(subs?.arr_minor), n: count((subs?.active ?? 0) + (subs?.trialing ?? 0) + (subs?.past_due ?? 0)) }) }}</template>
    </KpiCard>
    <KpiCard testid="arpu" :label="t('dash.kpi.arpu')" :delta="k.dArpu" :previous="block.prev_arpu_minor != null ? money(block.prev_arpu_minor) : undefined">
      <template #value>{{ block.arpu_minor == null ? "—" : money(block.arpu_minor) }}</template>
      <template #sub>{{ t("dash.kpi.arpuSub") }}</template>
    </KpiCard>
    <KpiCard testid="customers" :label="t('dash.kpi.customers')" :delta="k.dCustomers" :previous="data.customers ? count(data.customers.prev_new) : undefined">
      <template #value>{{ customersDown ? "—" : count(data.customers?.new) }}</template>
      <template #sub>{{ customersDown ? t("product.unavailable") : t("dash.kpi.customersSub", { total: count(data.customers?.total) }) }}</template>
    </KpiCard>
    <KpiCard testid="churn" good="down" :label="t('dash.kpi.churn')" :delta="k.dChurn" :previous="k.prevChurn !== null ? pct(k.prevChurn) : undefined">
      <template #value>{{ down("subscriptions") ? "—" : pct(k.churn) }}</template>
      <template #sub>{{ down("subscriptions") ? t("product.unavailable") : t("dash.kpi.churnSub", { n: count(subs?.churned), mrr: money(subs?.churned_mrr_minor) }) }}</template>
    </KpiCard>
    <KpiCard testid="failed-rate" good="down" :label="t('dash.kpi.failedRate')" :delta="k.dRate" :previous="k.prevRate !== null ? pct(k.prevRate) : undefined">
      <template #value>{{ down("payments") ? "—" : pct(k.rate) }}</template>
      <template #sub>{{ down("payments") ? t("product.unavailable") : t("dash.kpi.failedSub", { failed: count(pay?.failed), attempts: count((pay?.paid ?? 0) + (pay?.failed ?? 0)) }) }}</template>
    </KpiCard>
    <KpiCard testid="refunds" good="down" :label="t('dash.kpi.refunds')" :delta="k.dRefunds" :previous="prevRev ? money(prevRev.refunds_minor) : undefined">
      <template #value>{{ money(rev?.refunds_minor) }}</template>
      <template #sub>{{ t("dash.kpi.refundsSub", { tax: money(rev?.tax_minor) }) }}</template>
    </KpiCard>
    <KpiCard testid="open" good="down" :label="t('dash.kpi.open')">
      <template #value>{{ down("invoices") ? "—" : money(block.open_minor) }}</template>
      <template #sub>
        <Link v-if="!down('invoices')" :to="`${base}/invoices`" class="hover:text-foreground underline-offset-2 hover:underline">{{ t("dash.kpi.openSub", { n: count(block.open_count) }, block.open_count ?? 0) }}</Link>
        <template v-else>{{ t("product.unavailable") }}</template>
      </template>
    </KpiCard>
  </div>
</template>
