<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import type { CurrencySummary } from "./api";
import BarSeries from "./BarSeries.vue";
import DonutChart from "./DonutChart.vue";

/** One currency of the summary: the figures, the expenses by category, the series and the coupons. Money of different currencies is never added up, so each has its card. */
const props = defineProps<{ summary: CurrencySummary; granularity: "day" | "month"; canExport: boolean }>();
const emit = defineEmits<{ exportCoupons: [] }>();
const { t } = useI18n();
const fmt = useFormat();

const money = (v: string | number | undefined) => fmt.amount(v, props.summary.currency);
const profitIsLoss = computed(() => (props.summary.profit_minor ?? "").startsWith("-"));

/** The figures in the order they are read; discounts and sales appear only when a coupon took something off, and fees only say so when the provider reported none. */
const stats = computed(() => {
  const c = props.summary;
  const discounted = !!c.discounts_minor && c.discounts_minor !== "0";
  return [
    { key: "income", minor: c.gross_income_minor },
    { key: "tax", minor: c.tax_minor },
    ...(discounted ? [{ key: "discounts", minor: c.discounts_minor, hint: "discountsHint" }, { key: "sales", minor: c.sales_minor }] : []),
    { key: "refunds", minor: c.refunds_minor },
    { key: "net", minor: c.net_income_minor },
    { key: "fees", minor: c.provider_fees_minor, missing: !c.provider_fees_recorded, hint: c.provider_fees_recorded ? undefined : "feesHint" },
    { key: "expenses", minor: c.expenses_minor },
  ];
});
</script>

<template>
  <section class="mb-6" :aria-label="summary.currency">
    <Card>
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <h3 class="text-lg font-semibold">{{ summary.currency }}</h3>
        <span class="text-muted-foreground text-sm">{{ t("billing.accounting.paidInvoices", { count: Number(summary.paid_invoices ?? 0) }) }}</span>
      </div>
      <dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div v-for="s in stats" :key="s.key" :data-stat="s.key">
          <dt class="text-muted-foreground text-sm" :title="!s.missing && s.hint ? t(`billing.accounting.stat.${s.hint}`) : undefined">{{ t(`billing.accounting.stat.${s.key}`) }}</dt>
          <dd v-if="s.missing" class="text-muted-foreground text-xl" :title="s.hint ? t(`billing.accounting.stat.${s.hint}`) : undefined">{{ t("billing.accounting.stat.feesNotRecorded") }}</dd>
          <dd v-else class="text-xl font-semibold tabular-nums">{{ money(s.minor) }}</dd>
        </div>
        <div data-stat="profit" class="sm:col-span-2">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.profit") }}</dt>
          <dd class="text-2xl font-semibold tabular-nums" :class="profitIsLoss ? 'text-destructive' : 'text-success'">{{ money(summary.profit_minor) }}</dd>
        </div>
      </dl>

      <div class="grid gap-6 lg:grid-cols-2">
        <div>
          <h4 class="mb-2 text-sm font-medium">{{ t("billing.accounting.donut.title") }}</h4>
          <DonutChart v-if="(summary.expense_by_category ?? []).length" :items="summary.expense_by_category ?? []" :currency="summary.currency ?? ''" />
          <p v-else class="text-muted-foreground text-sm">{{ t("billing.accounting.donut.none") }}</p>
        </div>
        <div>
          <h4 class="mb-2 text-sm font-medium">{{ t("billing.accounting.bars.title") }}</h4>
          <BarSeries :points="summary.series ?? []" :currency="summary.currency ?? ''" :granularity="granularity" />
        </div>
      </div>

      <div v-if="(summary.coupons ?? []).length" data-section="coupons">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-medium">{{ t("billing.accounting.coupons.title") }}</h4>
          <Button type="button" variant="outline" size="sm" :disabled="!canExport" @click="emit('exportCoupons')">{{ t("billing.accounting.coupons.export") }}</Button>
        </div>
        <p class="text-muted-foreground text-xs">{{ t("billing.accounting.coupons.hint") }}</p>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("billing.accounting.coupons.coupon") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.accounting.coupons.invoices") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.accounting.coupons.gross") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.accounting.coupons.discount") }}</TableHead>
              <TableHead class="text-end">{{ t("billing.accounting.coupons.net") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="k in summary.coupons" :key="k.coupon_code">
              <TableCell class="mono font-medium">{{ k.coupon_code }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ k.invoices }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ money(k.gross_minor) }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ money(k.discount_minor) }}</TableCell>
              <TableCell class="text-end tabular-nums">{{ money(k.net_minor) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </Card>
  </section>
</template>
