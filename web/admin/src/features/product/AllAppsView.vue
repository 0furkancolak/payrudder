<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ArrowUpRight } from "@lucide/vue";
import { useQueries } from "@tanstack/vue-query";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { tenantApi, unwrap } from "@/shared/api/client";
import { PAGE } from "@/shared/api/limits";
import { tenantOf, type Environment } from "@/shared/lib/environment";
import { useFormat } from "@/shared/lib/format";
import { formatMinor, maskEmail } from "@/shared/lib/money";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useApps } from "./api";

/**
 * The pages of an environment over every app (`?app=all`): the lists that make sense across apps (customers, subscriptions, invoices, payments, packages
 * and meters) read each app's own data and show it as one table with the app in the first column; a row leads to the same record in its own app. A page
 * that only makes sense inside one app says so and offers the apps. Nothing is summed across apps here (the dashboard adds apps only within a currency).
 */
const props = defineProps<{ page: string; env: Environment }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const apps = useApps();

type Row = Record<string, unknown>;
interface Column {
  key: string;
  label: string;
  cell: (r: Row) => string;
  end?: boolean;
}
interface Source {
  /** The page under the tenant (what the row opens). */
  path: string;
  fetch: (tenant: string) => Promise<Row[]>;
  columns: Column[];
  title: string;
}

const text = (v: unknown): string => (typeof v === "string" || typeof v === "number" ? String(v) : "");
const money = (r: Row, amount: string): string => (r[amount] !== undefined ? formatMinor(text(r[amount]), text(r.currency), locale.value) : "");

const sources = computed<Record<string, Source>>(() => ({
  customers: {
    path: "customers",
    title: t("shell.items.customers"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/customers", { params: { query: { page_size: PAGE.whole } } }))).customers ?? []) as Row[],
    columns: [
      { key: "name", label: t("billing.customers.name"), cell: (r) => text(r.name) || text(r.external_id) },
      { key: "email", label: t("billing.customers.email"), cell: (r) => maskEmail(text(r.email)) },
      { key: "country", label: t("billing.customers.country"), cell: (r) => text(r.country) },
      { key: "currency", label: t("billing.customers.columns.currency"), cell: (r) => text(r.billing_currency) },
      { key: "created", label: t("billing.customers.columns.created"), cell: (r) => fmt.date(text(r.create_time), "day", "") },
    ],
  },
  subscriptions: {
    path: "subscriptions",
    title: t("shell.items.subscriptions"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/subscriptions", { params: { query: { page_size: PAGE.long } } }))).subscriptions ?? []) as Row[],
    columns: [
      { key: "customer", label: t("billing.subscriptions.customer"), cell: (r) => text(r.customer_external_id) },
      { key: "package", label: t("billing.subscriptions.package"), cell: (r) => `${text(r.plan_code)} v${text(r.plan_version)}` },
      { key: "currency", label: t("billing.subscriptions.currency"), cell: (r) => text(r.currency) },
      { key: "since", label: t("billing.subscriptions.since"), cell: (r) => fmt.date(text(r.start_time), "day", "") },
    ],
  },
  invoices: {
    path: "invoices",
    title: t("shell.items.invoices"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/invoices", { params: { query: { page_size: PAGE.long } } }))).invoices ?? []) as Row[],
    columns: [
      { key: "number", label: t("product.billing.invoices.number"), cell: (r) => text(r.number) || t("product.billing.invoices.draft") },
      { key: "customer", label: t("product.billing.invoices.customer"), cell: (r) => text(r.customer_external_id) },
      { key: "status", label: t("product.billing.invoices.status"), cell: (r) => text(r.status) },
      { key: "total", label: t("product.billing.invoices.total"), cell: (r) => money(r, "total_minor"), end: true },
      { key: "created", label: t("product.billing.invoices.created"), cell: (r) => fmt.date(text(r.create_time), "day", "") },
    ],
  },
  payments: {
    path: "payments",
    title: t("shell.items.payments"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/payments", { params: { query: { limit: PAGE.whole } } }))).payments ?? []) as Row[],
    columns: [
      { key: "kind", label: t("product.billing.payments.kind"), cell: (r) => text(r.kind) },
      { key: "provider", label: t("billing.subscriptions.provider"), cell: (r) => text(r.provider) },
      { key: "status", label: t("product.billing.invoices.status"), cell: (r) => text(r.status) },
      { key: "amount", label: t("scope.amount"), cell: (r) => money(r, "amount_minor"), end: true },
      { key: "when", label: t("scope.date"), cell: (r) => fmt.date(text(r.occurred_at), "day", "") },
    ],
  },
  packages: {
    path: "packages",
    title: t("shell.items.packages"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/plans"))).plans ?? []) as Row[],
    columns: [
      { key: "code", label: t("billing.packages.code"), cell: (r) => text(r.code) },
      { key: "name", label: t("billing.packages.name"), cell: (r) => text(r.name) },
    ],
  },
  meters: {
    path: "meters",
    title: t("shell.items.meters"),
    fetch: async (id) => ((await unwrap(tenantApi(id).GET("/v1/meters"))).meters ?? []) as Row[],
    columns: [
      { key: "code", label: t("billing.meters.code"), cell: (r) => text(r.code) },
      { key: "category", label: t("billing.meters.category"), cell: (r) => text(r.category) },
    ],
  },
}));
const source = computed(() => sources.value[props.page]);

/** The apps that have this environment (every app has a sandbox; a live one only once it exists). */
const targets = computed(() =>
  (apps.data.value?.apps ?? []).flatMap((a) => {
    const tenant = tenantOf(a, props.env);
    return tenant && a.id ? [{ id: a.id, name: a.name ?? a.id, tenant }] : [];
  }),
);

const results = useQueries({
  queries: computed(() =>
    source.value
      ? targets.value.map((a) => ({
          queryKey: ["all-apps", props.page, a.tenant] as const,
          queryFn: () => (source.value as Source).fetch(a.tenant),
        }))
      : [],
  ),
});

const loading = computed(() => apps.isPending.value || results.value.some((r) => r.isPending));
const rows = computed(() =>
  targets.value.flatMap((a, i) => ((results.value[i]?.data ?? []) as Row[]).map((r, n) => ({ app: a, row: r, key: `${a.id}-${n}` }))),
);
const failed = computed(() => targets.value.filter((_, i) => results.value[i]?.isError));
</script>

<template>
  <Card v-if="source" class="p-3" data-testid="all-apps-view" :data-page="page">
    <p class="text-muted-foreground px-3 pt-1 pb-3 text-sm" data-testid="all-apps-hint">{{ t("scope.allHint", { env: t(`product.env.${env}`) }) }}</p>
    <ErrorText v-for="a in failed" :key="a.id" :error="new Error(t('scope.failed', { app: a.name }))" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("scope.app") }}</TableHead>
          <TableHead v-for="c in source.columns" :key="c.key" :class="c.end ? 'text-end' : ''">{{ c.label }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("scope.open") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <template v-if="loading">
          <TableRow v-for="n in 4" :key="n" class="hover:bg-transparent" data-testid="all-apps-skeleton">
            <TableCell v-for="c in source.columns.length + 2" :key="c"><Skeleton class="h-3.5 w-20" /></TableCell>
          </TableRow>
        </template>
        <TableRow v-for="r in rows" :key="r.key" :data-testid="`all-apps-row-${r.app.id}`">
          <TableCell class="font-medium" data-testid="all-apps-app">{{ r.app.name }}</TableCell>
          <TableCell v-for="c in source.columns" :key="c.key" :class="c.end ? 'text-end tabular-nums' : ''">{{ c.cell(r.row) || "—" }}</TableCell>
          <TableCell class="text-end">
            <Link :to="`/app/t/${r.app.tenant}/${source.path}`" :search="{ app: r.app.id } as never" class="text-primary inline-flex items-center gap-1 text-sm font-medium hover:underline" :aria-label="`${t('scope.open')} ${r.app.name}`">
              <ArrowUpRight class="size-4" aria-hidden="true" />
            </Link>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!loading && rows.length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("scope.noData") }}</p>
  </Card>

  <Card v-else class="flex flex-col gap-3 p-6" data-testid="all-apps-choose">
    <h2 class="text-lg font-semibold tracking-tight">{{ t("scope.chooseTitle") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("scope.chooseHint", { page: t(`shell.items.${page}`) }) }}</p>
    <ul class="flex flex-wrap gap-2">
      <li v-for="a in targets" :key="a.id">
        <Link :to="`/app/t/${a.tenant}/${page === 'paymentLinks' ? 'payment-links' : page}`" :search="{ app: a.id } as never" class="bg-card border-border/60 shadow-card hover:bg-accent inline-flex h-10 items-center rounded-xl border px-4 text-sm font-medium" :data-testid="`choose-app-${a.id}`">{{ a.name }}</Link>
      </li>
    </ul>
  </Card>
</template>
