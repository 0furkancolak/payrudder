<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Activity, CalendarClock, CreditCard, FileText, Repeat, Rocket, TriangleAlert, Webhook } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { Donut } from "@/shared/ui/donut";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useDashboard, type DashSource } from "./api";
import BarList from "./BarList.vue";
import CurrencyTabs from "./CurrencyTabs.vue";
import KpiCard from "./KpiCard.vue";
import { bpPercent, chosenCurrency, delta, hasActivity, type DashCurrency, type DashQuery } from "./lib";
import PeriodBar from "./PeriodBar.vue";
import RevenueChart from "./RevenueChart.vue";

/**
 * The dashboard of one environment: a period, compared with the one before, in one block per currency (the tabs), with the customers, usage, renewals,
 * activity and health that are not about one currency. It asks the gateway once (`useDashboard`); a section whose service did not answer is named and the
 * rest stays. `base` is where the pages of the environment live (for the links), `source` which environment is shown.
 */
const props = defineProps<{ source: DashSource | null; base: string; query: DashQuery }>();
const emit = defineEmits<{ "update:query": [DashQuery] }>();
const { t, locale } = useI18n();
const errorText = useErrorText();

const dash = useDashboard(
  () => props.source,
  () => props.query,
);
const data = computed(() => dash.data.value);
const blocks = computed(() => data.value?.currencies ?? []);
const block = computed<DashCurrency | undefined>(() => chosenCurrency(blocks.value, props.query.cur));
const cur = computed(() => block.value?.currency ?? data.value?.default_currency ?? "");
const compare = computed(() => data.value?.range?.compare ?? props.query.compare);
const today = new Date().toLocaleDateString("en-CA");
const loading = computed(() => dash.isPending.value && !data.value);
const stale = computed(() => dash.isFetching.value && !!data.value);

const money = (minor: number | null | undefined) => formatMinor(minor ?? 0, cur.value, locale.value);
const count = (n: number | null | undefined) => new Intl.NumberFormat(locale.value).format(n ?? 0);
const pct = (v: number | null) => (v === null ? "—" : `${new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(v)}%`);
const dayText = (iso: string) => new Intl.DateTimeFormat(locale.value, { day: "numeric", month: "short" }).format(new Date(iso));
const when = (iso: string) => new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));

const b = computed(() => block.value);
const rev = computed(() => b.value?.revenue);
const prevRev = computed(() => (compare.value ? b.value?.prev_revenue : null));
const subs = computed(() => b.value?.subscriptions);
const prevSubs = computed(() => (compare.value ? b.value?.prev_subscriptions : null));
const pay = computed(() => b.value?.payments);
const prevPay = computed(() => (compare.value ? b.value?.prev_payments : null));

const attempts = computed(() => (pay.value?.paid ?? 0) + (pay.value?.failed ?? 0) + (pay.value?.processing ?? 0));
const rate = computed(() => bpPercent(pay.value?.failed_rate_bp));
const prevRate = computed(() => bpPercent(prevPay.value?.failed_rate_bp));
const churn = computed(() => bpPercent(subs.value?.churn_rate_bp));
const prevChurn = computed(() => bpPercent(prevSubs.value?.churn_rate_bp));
const spark = computed(() => (b.value?.series ?? []).map((p) => p.net_minor ?? 0));
const points = computed(() =>
  (b.value?.series ?? []).map((p) => ({ date: p.date ?? "", value: p.net_minor ?? 0, ...(p.prev_net_minor !== undefined && p.prev_net_minor !== null ? { prevDate: p.prev_date ?? "", prev: p.prev_net_minor } : {}) })),
);

const dNet = computed(() => (prevRev.value ? delta(rev.value?.net_minor, prevRev.value.net_minor) : null));
const dMrr = computed(() => (prevSubs.value ? delta(subs.value?.mrr_minor, prevSubs.value.mrr_minor) : null));
const dArpu = computed(() => (compare.value && b.value?.prev_arpu_minor !== null && b.value?.prev_arpu_minor !== undefined && b.value?.arpu_minor !== null ? delta(b.value?.arpu_minor, b.value?.prev_arpu_minor) : null));
const dCustomers = computed(() => (compare.value && data.value?.customers ? delta(data.value.customers.new, data.value.customers.prev_new) : null));
const dChurn = computed(() => (prevChurn.value !== null && churn.value !== null ? delta(churn.value, prevChurn.value) : null));
const dRate = computed(() => (prevRate.value !== null && rate.value !== null ? delta(rate.value, prevRate.value) : null));
const dRefunds = computed(() => (prevRev.value ? delta(rev.value?.refunds_minor, prevRev.value.refunds_minor) : null));

const segments = computed(() => [
  { key: "paid", value: pay.value?.paid ?? 0, color: "var(--brand)" },
  { key: "processing", value: pay.value?.processing ?? 0, color: "oklch(0.82 0.12 85)" },
  { key: "failed", value: pay.value?.failed ?? 0, color: "oklch(0.7 0.16 25)" },
  { key: "refunded", value: pay.value?.refunds ?? 0, color: "oklch(0.72 0.05 260)" },
]);
const segmentColor = (k: string) => segments.value.find((s) => s.key === k)?.color ?? "var(--muted)";

const topCustomers = computed(() => (b.value?.top_customers ?? []).map((c) => ({ key: c.customer_id ?? "", label: c.name || c.customer_external_id || "—", sub: c.name ? c.customer_external_id : "", value: c.income_minor ?? 0, text: money(c.income_minor) })));
const topPackages = computed(() => (b.value?.top_packages ?? []).map((p) => ({ key: p.plan_code ?? "", label: p.plan_code ?? "—", sub: t("dash.top.subs", { n: p.active ?? 0 }, p.active ?? 0), value: p.mrr_minor ?? 0, text: t("dash.top.perMonth", { amount: money(p.mrr_minor) }) })));
const aging = computed(() => (b.value?.aging ?? []).map((a) => ({ key: a.bucket ?? "", label: t(`dash.aging.${a.bucket}`), sub: t("dash.aging.count", { n: a.count ?? 0 }, a.count ?? 0), value: a.amount_minor ?? 0, text: money(a.amount_minor) })));
const usageMeters = computed(() => data.value?.usage?.meters ?? []);

const down = (name: string) => data.value?.unavailable?.includes(name) ?? false;
const empty = computed(() => !!data.value && !!b.value && !hasActivity(b.value) && (data.value.customers?.total ?? 0) === 0);

const activityIcon = (k?: string) => (k === "invoice" ? FileText : k === "payment" ? CreditCard : Repeat);
const activityStatus = (a: { kind?: string; status?: string }) => {
  if (a.kind === "invoice") return t(`product.invoiceStatus.${a.status}`);
  if (a.kind === "payment") return t(`product.status.${a.status}`);
  return t(`dash.activity.state.${a.status}`);
};
const activityTone = (a: { kind?: string; status?: string }) => (a.status === "past_due" ? "warning" : a.status === "cancelled" ? "neutral" : toneOf(a.status));
const activityText = (a: { kind?: string; label?: string; customer?: string }) =>
  a.kind === "invoice" ? t("dash.activity.invoice", { label: a.label ?? "" }) : a.kind === "payment" ? (a.label === "refund" ? t("dash.activity.refund") : t("dash.activity.payment")) : t("dash.activity.subscription", { plan: a.label ?? "" });
</script>

<template>
  <div class="flex flex-col gap-6" :aria-busy="loading || stale" data-testid="dashboard">
    <div class="flex flex-col gap-4">
      <PeriodBar :query="query" :today="today" @update:query="emit('update:query', $event)" />
      <CurrencyTabs
        v-if="blocks.length > 0"
        :currencies="blocks.map((x) => x.currency ?? '')"
        :active="cur"
        :default-currency="data?.default_currency ?? ''"
        @select="emit('update:query', { ...query, cur: $event })"
      />
    </div>

    <div v-if="dash.error.value && !data" class="bg-destructive-soft text-destructive flex flex-wrap items-center justify-between gap-3 rounded-2xl px-4 py-3 text-sm" role="alert" data-testid="dash-error">
      <span>{{ errorText(dash.error.value) }}</span>
      <Button size="sm" variant="outline" @click="dash.refetch()">{{ t("dash.retry") }}</Button>
    </div>
    <p v-else-if="dash.error.value" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">{{ t("dash.stale") }}</p>

    <p v-if="data && data.unavailable && data.unavailable.length > 0" class="bg-warning-soft text-warning flex items-start gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="dash-degraded">
      <TriangleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      {{ t("dash.degraded", { sections: data.unavailable.map((s) => t(`dash.sections.${s}`)).join(", ") }) }}
    </p>

    <!-- loading: the shape of the page, not a spinner -->
    <template v-if="loading">
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4" data-testid="dash-skeleton">
        <Skeleton v-for="n in 8" :key="n" class="h-36 rounded-3xl" />
      </div>
      <div class="grid gap-5 xl:grid-cols-3"><Skeleton class="h-80 rounded-3xl xl:col-span-2" /><Skeleton class="h-80 rounded-3xl" /></div>
    </template>

    <!-- nothing yet -->
    <section v-else-if="empty" class="surface flex flex-col items-center gap-4 px-6 py-14 text-center" data-testid="dash-empty">
      <span class="bg-brand-soft text-brand-strong flex size-16 items-center justify-center rounded-3xl"><Rocket class="size-8" aria-hidden="true" /></span>
      <div>
        <h2 class="text-xl font-semibold tracking-tight">{{ t("dash.empty.title") }}</h2>
        <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">{{ t("dash.empty.body") }}</p>
      </div>
      <div class="flex flex-wrap justify-center gap-2">
        <Button as-child><Link :to="`${base}/customers`">{{ t("shell.items.customers") }}</Link></Button>
        <Button as-child variant="outline"><Link :to="`${base}/packages`">{{ t("shell.items.packages") }}</Link></Button>
      </div>
    </section>

    <template v-else-if="data && b">
      <!-- KPIs -->
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard testid="revenue" :label="t('dash.kpi.net')" :delta="dNet" :previous="prevRev ? money(prevRev.net_minor) : undefined" :spark="spark">
          <template #value>{{ money(rev?.net_minor) }}</template>
          <template #sub>{{ t("dash.kpi.netSub", { gross: money(rev?.gross_minor), refunds: money(rev?.refunds_minor) }) }}</template>
        </KpiCard>
        <KpiCard testid="mrr" :label="t('dash.kpi.mrr')" :delta="dMrr" :previous="prevSubs ? money(prevSubs.mrr_minor) : undefined">
          <template #value>{{ down("subscriptions") ? "—" : money(subs?.mrr_minor) }}</template>
          <template #sub>{{ down("subscriptions") ? t("product.unavailable") : t("dash.kpi.mrrSub", { arr: money(subs?.arr_minor), n: count((subs?.active ?? 0) + (subs?.trialing ?? 0) + (subs?.past_due ?? 0)) }) }}</template>
        </KpiCard>
        <KpiCard testid="arpu" :label="t('dash.kpi.arpu')" :delta="dArpu" :previous="b.prev_arpu_minor != null ? money(b.prev_arpu_minor) : undefined">
          <template #value>{{ b.arpu_minor == null ? "—" : money(b.arpu_minor) }}</template>
          <template #sub>{{ t("dash.kpi.arpuSub") }}</template>
        </KpiCard>
        <KpiCard testid="customers" :label="t('dash.kpi.customers')" :delta="dCustomers" :previous="data.customers ? count(data.customers.prev_new) : undefined">
          <template #value>{{ down("customers") || !data.customers ? "—" : count(data.customers.new) }}</template>
          <template #sub>{{ down("customers") || !data.customers ? t("product.unavailable") : t("dash.kpi.customersSub", { total: count(data.customers.total) }) }}</template>
        </KpiCard>
        <KpiCard testid="churn" good="down" :label="t('dash.kpi.churn')" :delta="dChurn" :previous="prevChurn !== null ? pct(prevChurn) : undefined">
          <template #value>{{ down("subscriptions") ? "—" : pct(churn) }}</template>
          <template #sub>{{ down("subscriptions") ? t("product.unavailable") : t("dash.kpi.churnSub", { n: count(subs?.churned), mrr: money(subs?.churned_mrr_minor) }) }}</template>
        </KpiCard>
        <KpiCard testid="failed-rate" good="down" :label="t('dash.kpi.failedRate')" :delta="dRate" :previous="prevRate !== null ? pct(prevRate) : undefined">
          <template #value>{{ down("payments") ? "—" : pct(rate) }}</template>
          <template #sub>{{ down("payments") ? t("product.unavailable") : t("dash.kpi.failedSub", { failed: count(pay?.failed), attempts: count((pay?.paid ?? 0) + (pay?.failed ?? 0)) }) }}</template>
        </KpiCard>
        <KpiCard testid="refunds" good="down" :label="t('dash.kpi.refunds')" :delta="dRefunds" :previous="prevRev ? money(prevRev.refunds_minor) : undefined">
          <template #value>{{ money(rev?.refunds_minor) }}</template>
          <template #sub>{{ t("dash.kpi.refundsSub", { tax: money(rev?.tax_minor) }) }}</template>
        </KpiCard>
        <KpiCard testid="open" good="down" :label="t('dash.kpi.open')">
          <template #value>{{ down("invoices") ? "—" : money(b.open_minor) }}</template>
          <template #sub>
            <Link v-if="!down('invoices')" :to="`${base}/invoices`" class="hover:text-foreground underline-offset-2 hover:underline">{{ t("dash.kpi.openSub", { n: count(b.open_count) }, b.open_count ?? 0) }}</Link>
            <template v-else>{{ t("product.unavailable") }}</template>
          </template>
        </KpiCard>
      </div>

      <!-- revenue and payments -->
      <div class="grid gap-5 xl:grid-cols-3">
        <section class="surface flex flex-col gap-4 p-6 xl:col-span-2" :aria-label="t('dash.chart.title')">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.chart.title") }}</h2>
            <p class="text-muted-foreground text-xs">{{ t("dash.chart.note") }}</p>
          </div>
          <p v-if="down('revenue')" class="text-muted-foreground py-10 text-center text-sm">{{ t("product.unavailable") }}</p>
          <RevenueChart v-else :points="points" :currency="cur" :granularity="data.range?.granularity ?? 'day'" :compare="compare" />
        </section>

        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.payments.title')" data-testid="payments-card">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.payments.title") }}</h2>
          <p v-if="down('payments')" class="text-muted-foreground py-10 text-center text-sm">{{ t("product.unavailable") }}</p>
          <template v-else>
            <div class="flex flex-wrap items-center justify-center gap-6 sm:flex-nowrap xl:flex-wrap 2xl:flex-nowrap">
              <Donut :segments="segments" :label="t('dash.payments.label', { n: attempts })">
                <span class="text-3xl font-semibold tabular-nums">{{ count(attempts) }}</span>
                <span class="text-muted-foreground text-xs">{{ t("dash.payments.attempts") }}</span>
              </Donut>
              <ul class="flex flex-col gap-2.5 text-sm">
                <li v-for="s in ['paid', 'processing', 'failed', 'refunded']" :key="s" class="flex items-center gap-2.5">
                  <span class="size-3 shrink-0 rounded-full" :style="{ background: segmentColor(s) }" aria-hidden="true" />
                  <span class="flex-1">{{ t(`product.status.${s}`) }}</span>
                  <span class="font-medium tabular-nums">{{ count(s === "paid" ? pay?.paid : s === "failed" ? pay?.failed : s === "processing" ? pay?.processing : pay?.refunds) }}</span>
                </li>
              </ul>
            </div>
            <p class="text-muted-foreground text-xs">{{ t("dash.payments.note") }}</p>
            <p v-if="data.payments_truncated" class="bg-warning-soft text-warning rounded-xl px-3 py-2 text-xs" role="note">{{ t("dash.payments.truncated") }}</p>
          </template>
        </section>
      </div>

      <!-- who and what -->
      <div class="grid gap-5 lg:grid-cols-2 xl:grid-cols-3">
        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.top.customers')" data-testid="top-customers">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.top.customers") }}</h2>
          <p v-if="down('invoices')" class="text-muted-foreground py-6 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="topCustomers.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.top.customersEmpty") }}</p>
          <BarList v-else :items="topCustomers" />
        </section>
        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.top.packages')" data-testid="top-packages">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.top.packages") }}</h2>
          <p v-if="down('subscriptions')" class="text-muted-foreground py-6 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="topPackages.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.top.packagesEmpty", { currency: cur }) }}</p>
          <BarList v-else :items="topPackages" />
        </section>
        <section class="surface flex flex-col gap-4 p-6 lg:col-span-2 xl:col-span-1" :aria-label="t('dash.aging.title')" data-testid="aging">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.aging.title") }}</h2>
          <p v-if="down('invoices')" class="text-muted-foreground py-6 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="aging.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.aging.empty") }}</p>
          <BarList v-else :items="aging" tone="warning" />
        </section>
      </div>

      <!-- usage, renewals, activity -->
      <div class="grid gap-5 lg:grid-cols-2 xl:grid-cols-3">
        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.usage.title')" data-testid="usage">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.usage.title") }}</h2>
          <p v-if="down('usage')" class="text-muted-foreground py-6 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="usageMeters.length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.usage.empty") }}</p>
          <template v-else>
            <p class="text-muted-foreground text-xs">{{ t("dash.usage.intro", { n: data.usage?.sampled ?? 0, period: data.usage?.period_id ?? "" }) }}</p>
            <ul class="flex flex-col gap-4">
              <li v-for="m in usageMeters" :key="m.meter_code" class="flex flex-col gap-2">
                <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
                  <span class="mono font-medium">{{ m.meter_code }}</span>
                  <span class="flex gap-1.5">
                    <StatusPill v-if="(m.over ?? 0) > 0" tone="danger">{{ t("dash.usage.over", { n: m.over }) }}</StatusPill>
                    <StatusPill v-if="(m.near ?? 0) > 0" tone="warning">{{ t("dash.usage.near", { n: m.near }) }}</StatusPill>
                    <StatusPill v-if="(m.over ?? 0) === 0 && (m.near ?? 0) === 0" tone="success">{{ t("dash.usage.ok") }}</StatusPill>
                  </span>
                </div>
                <ul class="flex flex-col gap-1.5">
                  <li v-for="c in m.top" :key="c.customer_external_id" class="flex items-center gap-3 text-xs">
                    <span class="w-24 shrink-0 truncate">{{ c.customer_external_id }}</span>
                    <span class="bg-muted h-2 flex-1 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="Math.min(100, c.percent ?? 0)" aria-valuemin="0" aria-valuemax="100" :aria-label="c.customer_external_id">
                      <span :class="['block h-full rounded-full', (c.percent ?? 0) >= 100 ? 'bg-destructive/70' : (c.percent ?? 0) >= 80 ? 'bg-warning/70' : 'bg-brand']" :style="{ width: `${Math.min(100, c.percent ?? 0)}%` }" />
                    </span>
                    <span class="w-14 shrink-0 text-end tabular-nums">{{ pct(c.percent ?? 0) }}</span>
                  </li>
                </ul>
              </li>
            </ul>
          </template>
        </section>

        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('dash.renewals.title')" data-testid="renewals">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.renewals.title") }}</h2>
          <p v-if="down('renewals')" class="text-muted-foreground py-6 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="(b.renewals ?? []).length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.renewals.empty", { currency: cur }) }}</p>
          <ul v-else class="divide-border/70 divide-y">
            <li v-for="r in b.renewals" :key="r.subscription_id" class="flex items-center gap-3 py-2.5">
              <span class="bg-brand-soft text-brand-strong flex w-14 shrink-0 flex-col items-center rounded-xl py-1 text-xs font-semibold" aria-hidden="true"><CalendarClock class="mb-0.5 size-3.5" />{{ dayText(r.renews_at ?? "") }}</span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ r.customer_external_id }}</p>
                <p class="text-muted-foreground truncate text-xs">{{ r.plan_code }}<template v-if="r.interval === 'year'"> · {{ t("billing.packages.perYear") }}</template></p>
              </div>
              <StatusPill :tone="r.state === 'past_due' ? 'warning' : r.state === 'trialing' ? 'info' : 'neutral'">{{ t(`dash.activity.state.${r.state}`) }}</StatusPill>
            </li>
          </ul>
          <p v-if="data.renewals_truncated" class="text-muted-foreground text-xs">{{ t("dash.renewals.truncated") }}</p>
          <p class="text-muted-foreground text-xs">{{ t("dash.renewals.note") }}</p>
        </section>

        <section class="surface flex flex-col gap-4 p-6 lg:col-span-2 xl:col-span-1" :aria-label="t('dash.activity.title')" data-testid="activity">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("dash.activity.title") }}</h2>
          <p v-if="(data.activity ?? []).length === 0" class="text-muted-foreground py-6 text-center text-sm">{{ t("dash.activity.empty") }}</p>
          <ul v-else class="divide-border/70 divide-y">
            <li v-for="a in data.activity" :key="`${a.kind}-${a.ref}`" class="flex items-center gap-3 py-2.5">
              <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><component :is="activityIcon(a.kind)" class="size-4" /></span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ activityText(a) }}</p>
                <p class="text-muted-foreground truncate text-xs">{{ a.customer ? `${a.customer} · ` : "" }}{{ when(a.at ?? "") }}</p>
              </div>
              <div class="flex flex-col items-end gap-1">
                <span v-if="a.amount_minor != null" class="text-sm font-medium tabular-nums">{{ formatMinor(a.kind === "payment" && a.status === "refunded" ? `-${a.amount_minor}` : a.amount_minor, a.currency, locale) }}</span>
                <StatusPill :tone="activityTone(a)">{{ activityStatus(a) }}</StatusPill>
              </div>
            </li>
          </ul>
        </section>
      </div>

      <!-- health -->
      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.health.title')" data-testid="health">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.health.title") }}</h2>
          <StatusPill :tone="toneOf(data.status)">{{ t(`product.appStatus.${data.status}`) }}</StatusPill>
        </div>
        <ul class="grid gap-4 text-sm md:grid-cols-2 xl:grid-cols-3">
          <li class="flex items-center gap-3">
            <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Activity class="size-4" /></span>
            <span class="flex-1">{{ t("product.health.payments") }}</span>
            <StatusPill :tone="data.health?.payments_reachable ? 'success' : 'danger'">{{ data.health?.payments_reachable ? t("product.health.up") : t("product.health.down") }}</StatusPill>
          </li>
          <li class="flex items-center gap-3">
            <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><CreditCard class="size-4" /></span>
            <span class="flex-1">{{ t("product.health.providers") }}</span>
            <span v-if="(data.health?.providers ?? []).length > 0" class="text-muted-foreground capitalize">{{ (data.health?.providers ?? []).map((p) => p.provider).join(", ") }}</span>
            <span v-else class="text-muted-foreground">{{ t("product.health.noProviders") }}</span>
          </li>
          <li class="flex items-center gap-3">
            <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Webhook class="size-4" /></span>
            <span class="flex-1">{{ t("product.health.webhooks") }}</span>
            <span v-if="down('webhooks')" class="text-muted-foreground">{{ t("product.unavailable") }}</span>
            <StatusPill v-else-if="(data.health?.webhook_failed ?? 0) > 0" tone="danger">{{ t("product.health.failedDeliveries", { n: `${data.health?.webhook_failed}${data.health?.webhook_failed_more ? "+" : ""}` }) }}</StatusPill>
            <Link v-else-if="(data.health?.webhook_endpoints ?? 0) === 0" :to="`${base}/webhooks`" class="text-primary text-sm font-medium hover:underline">{{ t("product.health.addEndpoint") }}</Link>
            <StatusPill v-else tone="success">{{ t("product.health.endpoints", { n: data.health?.webhook_endpoints ?? 0 }, data.health?.webhook_endpoints ?? 0) }}</StatusPill>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>
