<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useErrorText } from "@/shared/lib/errors";
import { label } from "@/shared/lib/labels";
import { tenantBase } from "@/shared/lib/session";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { Input } from "@/shared/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDunningReport, useRevenueReport, useSubscriptionReport } from "./api";

const { t, te } = useI18n();
const fmt = useFormat();
const errorText = useErrorText();
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
const money = (v: string | number | undefined, currency?: string) => fmt.amount(v, currency);

// Subscriptions
const month = ref("");
const subSplit = ref<string[]>([]);
const subs = useSubscriptionReport(tenantId, month, subSplit);
const toggleSub = (g: string) => (subSplit.value = subSplit.value.includes(g) ? subSplit.value.filter((x) => x !== g) : [...subSplit.value, g]);

// Dunning
const dunning = useDunningReport(tenantId);
const when = (v?: string) => fmt.date(v, "short", "—");
</script>

<template>
  <div class="mb-4 flex gap-2" role="tablist" :aria-label="t('billing.reports.kinds')">
    <Button v-for="k in tabs" :key="k" role="tab" :aria-selected="tab === k" :variant="tab === k ? 'default' : 'outline'" size="sm" @click="tab = k">{{ t(`billing.reports.${k}`) }}</Button>
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
    <p v-if="revenue.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(revenue.error.value) }}</p>
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
        <TableRow v-for="(r, i) in revenue.data.value?.rows ?? []" :key="i">
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
    <p v-if="subs.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(subs.error.value) }}</p>
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
        <TableRow v-for="(r, i) in subs.data.value?.rows ?? []" :key="i">
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
  </Card>

  <Card v-else class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.reports.dunning") }}</h2>
    <p class="text-muted-foreground px-3 text-xs">{{ t("billing.reports.dunningHint") }}</p>
    <p v-if="dunning.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(dunning.error.value) }}</p>
    <ul class="flex flex-wrap gap-2 px-3">
      <li v-for="(s, i) in dunning.data.value?.summary ?? []" :key="i">
        <Badge variant="secondary">{{ label(t, te, "dunningState", s.state) }} · {{ s.provider || "—" }} · {{ s.currency }}: {{ s.invoices }} · {{ money(s.total_minor, s.currency) }}</Badge>
      </li>
    </ul>
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
        <TableRow v-for="d in dunning.data.value?.invoices ?? []" :key="d.invoice_id">
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
  </Card>
</template>
