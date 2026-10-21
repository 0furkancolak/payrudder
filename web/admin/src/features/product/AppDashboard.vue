<script setup lang="ts">
import { Link, useNavigate, useParams, useSearch } from "@tanstack/vue-router";
import { ChevronLeft, ChevronRight, FlaskConical, Gift, Link2, Users, Webhook } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { Donut } from "@/shared/ui/donut";
import { Skeleton } from "@/shared/ui/skeleton";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Sparkline } from "@/shared/ui/sparkline";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useAccount, useApp, useOverview, type Environment } from "./api";
import HealthCard from "./HealthCard.vue";
import KeysPanel from "./KeysPanel.vue";
import PaymentsCard from "./PaymentsCard.vue";
import { PAYMENT_STATUSES, STATUS_COLORS, countOf, isEnvironment, revenueByCurrency, seriesOf, tenantOf, totalPayments } from "./lib";

const { t, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const search = useSearch({ strict: false });
const navigate = useNavigate();

const appId = computed(() => String(params.value.appId ?? ""));
const env = computed<Environment>(() => {
  const v = (search.value as { env?: string }).env;
  return isEnvironment(v) ? v : "live";
});
const app = useApp(appId);
const account = useAccount();
const overview = useOverview(appId, env);

const data = computed(() => overview.data.value);
const tenantId = computed(() => tenantOf(app.data.value?.app, env.value) || (data.value?.tenant_id ?? ""));
const hasLive = computed(() => tenantOf(app.data.value?.app, "live") !== "");
const environments = computed(() => [
  ...(hasLive.value || !app.data.value ? [{ value: "live", label: t("product.env.live") }] : []),
  { value: "sandbox", label: t("product.env.sandbox") },
]);
const canManage = computed(() => (app.data.value?.role ?? account.data.value?.role) !== "viewer");

function pickEnv(value: string) {
  if (isEnvironment(value)) void navigate({ to: "/app/apps/$appId", params: { appId: appId.value }, search: { env: value } as never, replace: true });
}

const revenue = computed(() => revenueByCurrency({ currency: data.value?.currency ?? "", payments: data.value?.payments }));
const lead = computed(() => revenue.value[0]);
const total = computed(() => totalPayments(data.value?.payments));
const failed = computed(() => countOf(data.value?.payments, "failed"));
const money = (minor: number | undefined, currency: string | undefined) => formatMinor(minor ?? 0, currency, locale.value);
const segments = computed(() => PAYMENT_STATUSES.map((s) => ({ key: s, value: countOf(data.value?.payments, s), color: STATUS_COLORS[s] ?? "var(--muted)" })));
const mrr = computed(() => (data.value?.subscriptions ?? []).filter((s) => (s.mrr_minor ?? 0) > 0));
const countText = (c?: { count?: number; more?: boolean }) => (c ? `${c.count ?? 0}${c.more ? "+" : ""}` : "—");

const links = computed(() => [
  { key: "customers", icon: Users, to: `/app/t/${tenantId.value}/customers` },
  { key: "paymentLinks", icon: Link2, to: `/app/t/${tenantId.value}/payment-links` },
  { key: "credits", icon: Gift, to: `/app/t/${tenantId.value}/credits` },
  { key: "webhooks", icon: Webhook, to: `/app/t/${tenantId.value}/webhooks` },
]);
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="flex flex-wrap items-end justify-between gap-4 px-1">
      <div class="min-w-0">
        <Link to="/app" class="text-muted-foreground hover:text-foreground mb-1 inline-flex items-center gap-1 text-sm"><ChevronLeft class="size-4 rtl:rotate-180" />{{ account.data.value?.name || t("product.nav.home") }}</Link>
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="truncate text-3xl font-semibold tracking-tight md:text-4xl">{{ app.data.value?.app?.name ?? "…" }}</h1>
          <StatusPill v-if="data" :tone="toneOf(data.status)">{{ t(`product.appStatus.${data.status}`) }}</StatusPill>
        </div>
        <p v-if="app.data.value?.app?.slug" class="text-muted-foreground mono mt-1 text-xs">{{ app.data.value.app.slug }}</p>
      </div>
      <SegmentedControl :model-value="env" :options="environments" :label="t('product.env.label')" @update:model-value="pickEnv" />
    </header>

    <p v-if="app.error.value || overview.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(app.error.value ?? overview.error.value) }}</p>
    <p v-if="env === 'sandbox'" class="bg-brand-soft text-brand-strong flex items-center gap-2 rounded-2xl px-4 py-3 text-sm" role="note"><FlaskConical class="size-4 shrink-0" />{{ t("product.env.sandboxNote") }}</p>
    <p v-if="data && data.unavailable && data.unavailable.length > 0" class="bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm" role="status">
      {{ t("product.degraded", { sections: data.unavailable.map((s) => t(`product.sections.${s}`)).join(", ") }) }}
    </p>

    <div v-if="overview.isPending.value" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4" aria-busy="true">
      <Skeleton v-for="n in 4" :key="n" class="h-36 rounded-3xl" />
    </div>

    <template v-if="data">
      <!-- KPIs -->
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
        <article class="surface flex flex-col justify-between gap-3 p-6" :aria-label="t('product.kpi.revenue')">
          <p class="text-muted-foreground text-sm">{{ t("product.kpi.revenueDays", { days: data.window_days ?? 30 }) }}</p>
          <div class="flex flex-col gap-2">
            <div class="min-w-0">
              <p v-if="lead" class="truncate text-3xl font-semibold tracking-tight tabular" data-testid="kpi-revenue">{{ money(lead.minor, lead.currency) }}</p>
              <p v-else-if="data.payments" class="text-3xl font-semibold tracking-tight tabular">{{ money(0, data.currency) }}</p>
              <p v-else class="text-muted-foreground text-sm">{{ t("product.unavailable") }}</p>
              <p v-if="revenue.length > 1" class="text-muted-foreground mt-0.5 truncate text-xs tabular">{{ revenue.slice(1).map((r) => money(r.minor, r.currency)).join(" · ") }}</p>
            </div>
            <Sparkline v-if="lead" class="w-full" :values="seriesOf(data.payments, lead.currency)" :width="220" :height="36" />
          </div>
        </article>
        <article class="surface flex flex-col justify-between gap-3 p-6" :aria-label="t('product.kpi.payments')">
          <p class="text-muted-foreground text-sm">{{ t("product.kpi.payments") }}</p>
          <div>
            <p class="text-3xl font-semibold tracking-tight tabular" data-testid="kpi-payments">{{ data.payments ? total : "—" }}</p>
            <p class="mt-0.5 text-xs" :class="failed > 0 ? 'text-destructive font-medium' : 'text-muted-foreground'">{{ t("product.kpi.failedCount", { n: failed }) }}</p>
          </div>
        </article>
        <article class="surface flex flex-col justify-between gap-3 p-6" :aria-label="t('product.kpi.openInvoices')">
          <p class="text-muted-foreground text-sm">{{ t("product.kpi.openInvoices") }}</p>
          <div>
            <p class="text-3xl font-semibold tracking-tight tabular" data-testid="kpi-open">{{ countText(data.invoices?.open) }}</p>
            <p class="text-muted-foreground mt-0.5 text-xs tabular">
              {{ (data.invoices?.revenue ?? []).filter((r) => (r.outstanding_minor ?? 0) > 0).map((r) => t("product.kpi.outstanding", { amount: money(r.outstanding_minor, r.currency) })).join(" · ") || t("product.kpi.paidInvoices", { n: countText(data.invoices?.paid) }) }}
            </p>
          </div>
        </article>
        <article class="surface flex flex-col justify-between gap-3 p-6" :aria-label="t('product.kpi.customers')">
          <p class="text-muted-foreground text-sm">{{ t("product.kpi.customers") }}</p>
          <div>
            <p class="text-3xl font-semibold tracking-tight tabular" data-testid="kpi-customers">{{ countText(data.customers ?? undefined) }}</p>
            <p class="text-muted-foreground mt-0.5 truncate text-xs tabular">{{ mrr.map((s) => t("product.kpi.mrr", { amount: money(s.mrr_minor, s.currency) })).join(" · ") || t("product.kpi.noMrr") }}</p>
          </div>
        </article>
      </div>

      <!-- Donut, revenue per currency, health -->
      <div class="grid gap-5 lg:grid-cols-3">
        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.donut.title')">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.donut.title") }}</h2>
          <p v-if="!data.payments" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.unavailable") }}</p>
          <div v-else class="flex flex-wrap items-center justify-center gap-6 sm:flex-nowrap">
            <Donut :segments="segments" :label="t('product.donut.label', { n: total })">
              <span class="text-3xl font-semibold tabular">{{ total }}</span>
              <span class="text-muted-foreground text-xs">{{ t("product.donut.center") }}</span>
            </Donut>
            <ul class="flex flex-col gap-2.5 text-sm">
              <li v-for="s in PAYMENT_STATUSES" :key="s" class="flex items-center gap-2.5">
                <span class="size-3 shrink-0 rounded-full" :style="{ background: STATUS_COLORS[s] }" aria-hidden="true" />
                <span class="flex-1">{{ t(`product.status.${s}`) }}</span>
                <span class="font-medium tabular">{{ countOf(data.payments, s) }}</span>
              </li>
            </ul>
          </div>
        </section>

        <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.currency.title')">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.currency.title") }}</h2>
          <p v-if="!data.invoices" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="data.invoices.revenue?.length === 0" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.currency.empty") }}</p>
          <ul v-else class="flex flex-col gap-4">
            <li v-for="r in data.invoices.revenue" :key="r.currency" class="flex flex-col gap-2">
              <div class="flex items-baseline justify-between">
                <span class="text-sm font-medium">{{ r.currency }}</span>
                <span class="text-muted-foreground text-xs tabular">{{ t("product.currency.billed", { amount: money(r.billed_minor, r.currency) }) }}</span>
              </div>
              <div class="bg-muted h-2.5 overflow-hidden rounded-full" role="img" :aria-label="t('product.currency.collectedOf', { collected: money(r.collected_minor, r.currency), billed: money(r.billed_minor, r.currency) })">
                <div class="bg-brand h-full rounded-full" :style="{ width: `${r.billed_minor ? Math.min(100, ((r.collected_minor ?? 0) / r.billed_minor) * 100) : 0}%` }" />
              </div>
              <div class="text-muted-foreground flex justify-between text-xs tabular">
                <span>{{ t("product.currency.collected", { amount: money(r.collected_minor, r.currency) }) }}</span>
                <span>{{ t("product.currency.outstanding", { amount: money(r.outstanding_minor, r.currency) }) }}</span>
              </div>
            </li>
          </ul>
        </section>

        <HealthCard :overview="data" :tenant-id="tenantId" />
      </div>

      <!-- Payments, invoices -->
      <div class="grid gap-5 lg:grid-cols-3">
        <PaymentsCard class="lg:col-span-2" :payments="data.payments" />
        <section class="surface flex flex-col gap-3 p-6" :aria-label="t('product.invoices.title')">
          <h2 class="text-lg font-semibold tracking-tight">{{ t("product.invoices.title") }}</h2>
          <p v-if="!data.invoices" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.unavailable") }}</p>
          <p v-else-if="data.invoices.recent?.length === 0" class="text-muted-foreground py-8 text-center text-sm">{{ t("product.invoices.empty") }}</p>
          <ul v-else class="divide-border/70 divide-y">
            <li v-for="i in data.invoices.recent" :key="i.id" class="flex items-center gap-3 py-3">
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ i.number || t("product.invoices.draft") }}</p>
                <p class="text-muted-foreground truncate text-xs">{{ i.customer_external_id }}</p>
              </div>
              <div class="flex flex-col items-end gap-1">
                <span class="text-sm font-medium tabular">{{ money(i.total_minor, i.currency) }}</span>
                <StatusPill :tone="toneOf(i.status)">{{ t(`product.invoiceStatus.${i.status}`) }}</StatusPill>
              </div>
            </li>
          </ul>
        </section>
      </div>
    </template>

    <!-- Keys and the way deeper -->
    <div class="grid gap-5 lg:grid-cols-3">
      <KeysPanel class="lg:col-span-2" :app-id="appId" :can-manage="canManage" />
      <section class="surface flex flex-col gap-3 p-6" :aria-label="t('product.links.title')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.links.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.links.intro", { env: t(`product.env.${env}`) }) }}</p>
        <ul class="flex flex-col gap-2">
          <li v-for="l in links" :key="l.key">
            <Link :to="l.to" class="hover:bg-accent focus-visible:bg-accent bg-muted/60 flex items-center gap-3 rounded-2xl px-4 py-3 text-sm font-medium transition-colors">
              <span class="bg-card text-brand-strong flex size-8 items-center justify-center rounded-xl" aria-hidden="true"><component :is="l.icon" class="size-4" /></span>
              <span class="flex-1">{{ t(`product.links.${l.key}`) }}</span>
              <ChevronRight class="text-muted-foreground size-4 rtl:rotate-180" aria-hidden="true" />
            </Link>
          </li>
        </ul>
      </section>
    </div>
  </section>
</template>
