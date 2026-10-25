<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import BarList from "./BarList.vue";
import DashSection from "./DashSection.vue";
import type { DashCurrency, Dashboard } from "./lib";

/** Who and what the money comes from: the customers that paid most, the packages with the most recurring revenue, and what is owed by how late it is. */
const props = defineProps<{ data: Dashboard; block: DashCurrency; cur: string }>();
const { t } = useI18n();
const fmt = useFormat();

const money = (minor: number | null | undefined) => fmt.amount(minor, props.cur);
const down = (name: string) => props.data.unavailable?.includes(name) ?? false;

const topCustomers = computed(() =>
  (props.block.top_customers ?? []).map((c) => ({ key: c.customer_id ?? "", label: c.name || c.customer_external_id || "—", sub: c.name ? c.customer_external_id : "", value: c.income_minor ?? 0, text: money(c.income_minor) })),
);
const topPackages = computed(() =>
  (props.block.top_packages ?? []).map((p) => ({
    key: p.plan_code ?? "",
    label: p.plan_code ?? "—",
    sub: t("dash.top.subs", { n: p.active ?? 0 }, p.active ?? 0),
    value: p.mrr_minor ?? 0,
    text: t("dash.top.perMonth", { amount: money(p.mrr_minor) }),
  })),
);
const aging = computed(() =>
  (props.block.aging ?? []).map((a) => ({ key: a.bucket ?? "", label: t(`dash.aging.${a.bucket}`), sub: t("dash.aging.count", { n: a.count ?? 0 }, a.count ?? 0), value: a.amount_minor ?? 0, text: money(a.amount_minor) })),
);
</script>

<template>
  <div class="grid gap-5 lg:grid-cols-2 xl:grid-cols-3">
    <DashSection :title="t('dash.top.customers')" testid="top-customers" :down="down('invoices')" :empty="topCustomers.length === 0" :empty-text="t('dash.top.customersEmpty')">
      <BarList :items="topCustomers" />
    </DashSection>
    <DashSection :title="t('dash.top.packages')" testid="top-packages" :down="down('subscriptions')" :empty="topPackages.length === 0" :empty-text="t('dash.top.packagesEmpty', { currency: cur })">
      <BarList :items="topPackages" />
    </DashSection>
    <DashSection :title="t('dash.aging.title')" testid="aging" class="lg:col-span-2 xl:col-span-1" :down="down('invoices')" :empty="aging.length === 0" :empty-text="t('dash.aging.empty')">
      <BarList :items="aging" tone="warning" />
    </DashSection>
  </div>
</template>
