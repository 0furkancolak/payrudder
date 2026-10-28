<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { useRouter } from "@tanstack/vue-router";
import { CreditCard, ArrowUpRight, Ban, CircleCheck, Gauge, Link2, MapPin, Receipt, Repeat, UserPen, Users } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { usePackages } from "@/features/packages";
import { useSubscriptionActions } from "@/features/subscriptions/api";
import CreateSubscriptionForm from "@/features/subscriptions/CreateSubscriptionForm.vue";
import { inRange, rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { useFormat } from "@/shared/lib/format";
import { formatMinor, maskEmail } from "@/shared/lib/money";
import { tenantBase } from "@/shared/lib/session";
import { useOpenParam } from "@/shared/lib/useOpenParam";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { useTableState } from "@/shared/lib/useTableState";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { Button } from "@/shared/ui/button";
import { downloadCsv } from "@/shared/lib/csv";
import { runBulk } from "@/shared/lib/bulk";
import { askConfirm } from "@/shared/lib/confirm";
import { SortSelect } from "@/shared/ui/sort-select";
import { nameAsc, nameDesc, newest, oldest, time } from "@/shared/lib/sortDefs";

import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions, type RowAction } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import CustomerQuotasSheet from "./CustomerQuotasSheet.vue";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useBlockCustomer, useCustomers, useCustomerSummaries } from "./api";
import CustomerAvatar from "./CustomerAvatar.vue";
import CustomerCreateSheet from "./CustomerCreateSheet.vue";
import CustomerEditSheets from "./CustomerEditSheets.vue";
import PaymentLinkSheet from "@/features/paymentlinks/PaymentLinkSheet.vue";
import CustomerSheet from "./CustomerSheet.vue";
import { countryName, emptySummary, flagOf, headline, type Customer, type CustomerTab, type SubscriptionStatus } from "./lib";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

/**
 * The customers of a tenant, after Stripe's customer list: who (initials, name, e-mail), how their subscriptions stand, currency and country, what they
 * spent, when they joined and last paid. The figures are worked out from the invoices, payments and subscriptions the console loads (useCustomerSummaries).
 * A row opens the customer's sheet; every form (new customer, address, tax data, new subscription) is a sheet of its own.
 */
const { t, locale } = useI18n();
const fmt = useFormat();
const tenantId = useRouteTenantId();
const router = useRouter();
const customers = useCustomers(tenantId);
const summaries = useCustomerSummaries(tenantId);
const list = computed(() => customers.data.value?.customers ?? []);
const summaryOf = (c: Customer) => summaries.byCustomer.value.get(c.external_id ?? "") ?? emptySummary();

const range = ref<DateRange>(thisMonth());
const table = useTableState(() => list.value.filter((c) => inRange(c.create_time, range.value)), {
  sheet: { kind: "customer", id: (c) => c.external_id, loaded: () => customers.isSuccess.value },
  selectId: (c) => c.external_id,
  searchFields: (c) => [c.external_id, c.name, c.email, c.tax_id, c.country, c.billing_currency],
  sorts: () => [newest(t, (c) => time(c.create_time)), oldest(t, (c) => time(c.create_time)), nameAsc(t, (c) => c.name || c.external_id), nameDesc(t, (c) => c.name || c.external_id)],
  url: true,
  filters: {
    country: (c, v) => (c.country ?? "") === v,
    currency: (c, v) => (c.billing_currency ?? "") === v,
    subscription: (c, v) => (v === "active" ? summaryOf(c).activeSubscriptions > 0 : summaryOf(c).activeSubscriptions === 0),
    billing: (c, v) => (v === "open" ? summaryOf(c).openInvoices > 0 : v === "delinquent" ? summaryOf(c).delinquent : true),
  },
});
watch(range, () => (table.page.value = 1), { deep: true });
const active = computed(() => table.active.value || rangeChanged(range.value));
function clear() {
  table.clear();
  range.value = thisMonth();
}
const filter = (key: string) => computed({ get: () => table.filters.value[key] ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const countryFilter = filter("country");
const currencyFilter = filter("currency");
const subscriptionFilter = filter("subscription");
const billingFilter = filter("billing");
const distinct = (pick: (c: Customer) => string | undefined) => computed(() => [...new Set(list.value.map((c) => pick(c) ?? "").filter(Boolean))].sort());
const countries = distinct((c) => c.country);
const currencies = distinct((c) => c.billing_currency);
const countryOptions = computed(() => [{ value: "", label: t("billing.customers.allCountries") }, ...countries.value.map((c) => ({ value: c, label: `${flagOf(c)} ${countryName(c, locale.value)}`.trim() }))]);
const currencyOptions = computed(() => [{ value: "", label: t("billing.customers.allCurrencies") }, ...currencies.value.map((c) => ({ value: c }))]);
const subscriptionOptions = computed(() => [
  { value: "", label: t("common.all") },
  { value: "active", label: t("billing.customers.filters.hasActive") },
  { value: "none", label: t("billing.customers.filters.noActive") },
]);
const billingOptions = computed(() => [
  { value: "", label: t("common.all") },
  { value: "open", label: t("billing.customers.filters.openInvoices") },
  { value: "delinquent", label: t("billing.customers.filters.delinquent") },
]);

const STATUS_TONES: Record<SubscriptionStatus, Tone> = { pastDue: "danger", active: "success", trialing: "info", paused: "warning", cancelled: "neutral", none: "neutral" };
const spendOf = (c: Customer) => headline(summaryOf(c).spend, c.billing_currency);

// --- the sheet of one customer
const current = table.row;
const currentId = computed(() => current.value?.external_id ?? "");
const tab = ref<CustomerTab>("overview");
function open(i: number, how: { tab?: CustomerTab } = {}) {
  tab.value = how.tab ?? "overview";
  table.open(i);
}
// Stepping to another customer keeps the tab; closing the sheet starts the next one on the overview.
watch(table.detail, (v) => {
  if (v === null) tab.value = "overview";
});
// A link from an invoice, a payment or a subscription opens the customer it names; a filter that hides it is cleared first.
useOpenParam("customer", () => customers.isSuccess.value, (id) => {
  const is = (c: Customer) => c.external_id === id;
  if (!table.openWhere(is)) {
    clear();
    table.openWhere(is);
  }
});

// --- what the sheet and the row actions lead to
const creating = ref(false);
const editingContact = ref<Customer | null>(null);
const editingAddress = ref<Customer | null>(null);
const editingTax = ref<Customer | null>(null);
const packages = usePackages(tenantId);
const act = useSubscriptionActions(tenantId);
const blocking = useBlockCustomer(tenantId);
const bulkNote = ref("");
async function setBlocked(c: Customer, on: boolean) {
  if (on && !(await askConfirm({ body: t("billing.customers.block.confirm", { name: c.name || c.external_id }) }))) return;
  if (on) await blocking.block.mutateAsync({ externalId: c.external_id ?? "" });
  else await blocking.unblock.mutateAsync(c.external_id ?? "");
}
/** The same change to every ticked customer; a failure on one does not stop the rest, and the ones not changed are named. */
async function bulkBlock(on: boolean) {
  const rows = table.selectedRows.value.filter((c) => (c.block_time ? 1 : 0) !== (on ? 1 : 0));
  if (rows.length === 0) return;
  if (on && !(await askConfirm({ body: t("billing.customers.block.confirmMany", { n: rows.length }) }))) return;
  const r = await runBulk(rows, (c) => (on ? blocking.block.mutateAsync({ externalId: c.external_id ?? "" }) : blocking.unblock.mutateAsync(c.external_id ?? "")));
  for (const c of r.ok) table.tick(c, false);
  bulkNote.value = t("common.bulk.done", { ok: r.ok.length, failed: r.failed.length }) + (r.failed.length ? ` · ${t("common.bulk.notChanged")}: ${r.failed.map((f) => f.row.external_id).join(", ")}` : "");
}
const subscribing = ref(false);
const subscribeFor = ref("");
function newSubscription(c: Customer) {
  subscribeFor.value = c.external_id ?? "";
  subscribing.value = true;
}
// The payment link form opens over the customer's sheet, with the customer filled in, rather than taking the reader to the payment links page.
const linking = ref(false);
const linkFor = ref("");
function paymentLink(externalId?: string) {
  linkFor.value = externalId ?? "";
  linking.value = true;
}
function toCredits() {
  void router.navigate({ to: `${tenantBase()}/$id/credits`, params: { id: tenantId.value } as never });
}
// --- the customer's own quotas (overrides of its package's limits)
const quotasOf = ref("");
const quotasOpen = computed({
  get: () => quotasOf.value !== "",
  set: (v) => {
    if (!v) quotasOf.value = "";
  },
});
// The forms of a customer are in the address too: a reload or a shared link opens the one it names once the customers are loaded.
const customerOf = (id: string) => list.value.find((c) => c.external_id === id);
const openOf = (target: typeof editingAddress) =>
  computed({
    get: () => target.value !== null,
    set: (v) => {
      if (!v) target.value = null;
    },
  });
useSheetTarget("customer-contact", openOf(editingContact), editingContact, { id: (c) => c.external_id ?? "", find: customerOf });
useSheetTarget("customer-address", openOf(editingAddress), editingAddress, { id: (c) => c.external_id ?? "", find: customerOf });
useSheetTarget("customer-tax", openOf(editingTax), editingTax, { id: (c) => c.external_id ?? "", find: customerOf });
const quotaTarget = computed({ get: () => (quotasOf.value === "" ? null : quotasOf.value), set: (v) => (quotasOf.value = v ?? "") });
useSheetTarget("customer-quotas", quotasOpen, quotaTarget, { id: (c) => c, find: (id) => (customerOf(id) ? id : undefined) });
function actionsOf(c: Customer, i: number): RowAction[] {
  return [
    { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => open(i) },
    { key: "subscription", label: t("billing.customers.sheet.newSubscription"), icon: Repeat, onSelect: () => newSubscription(c) },
    { key: "portal", label: t("billing.paymentLinks.portal.open"), icon: Receipt, onSelect: () => open(i) },
    { key: "link", label: t("billing.paymentLinks.paymentLink"), icon: Link2, onSelect: () => paymentLink(c.external_id) },
    { key: "contact", label: t("billing.customers.contact.edit"), icon: UserPen, separatorBefore: true, onSelect: () => (editingContact.value = c) },
    { key: "address", label: t("billing.customers.address.edit"), icon: MapPin, onSelect: () => (editingAddress.value = c) },
    { key: "tax", label: t("einvoice.customer.edit"), icon: CreditCard, onSelect: () => (editingTax.value = c) },
    { key: "quotas", label: t("billing.quotas.action"), icon: Gauge, onSelect: () => (quotasOf.value = c.external_id ?? "") },
    c.block_time
      ? { key: "unblock", label: t("billing.customers.block.lift"), icon: CircleCheck, separatorBefore: true, onSelect: () => setBlocked(c, false) }
      : { key: "block", label: t("billing.customers.block.action"), icon: Ban, separatorBefore: true, onSelect: () => setBlocked(c, true) },
  ];
}

/** The ticked customers as a CSV file: the fields the list knows, no secret. */
function exportSelected() {
  downloadCsv(
    "customers.csv",
    ["external_id", "name", "email", "country", "billing_currency", "tax_id", "created"],
    table.selectedRows.value.map((c) => [c.external_id, c.name ?? "", c.email ?? "", c.country ?? "", c.billing_currency ?? "", c.tax_id ?? "", c.create_time ?? ""]),
  );
}
</script>

<template>
  <HeaderAction test-id="customers-new" :label="t('billing.customers.new')" @click="creating = true" />
  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.name || currentId" wide>
    <CustomerSheet
      v-if="current"
      :key="currentId"
      v-model:tab="tab"
      :tenant-id="tenantId"
      :customer="current"
      :summary="summaryOf(current)"
      :summary-loading="summaries.loading.value"
      @edit-contact="editingContact = current"
      @edit-address="editingAddress = current"
      @edit-tax="editingTax = current"
      @new-subscription="newSubscription(current)"
      @payment-link="paymentLink(currentId)"
      @credits="toCredits"
    />
  </DetailSheet>
  <!-- After the customer's sheet: a sheet portals to where it was first rendered, so these open above the one they are opened from. -->
  <CustomerCreateSheet v-model:open="creating" :tenant-id="tenantId" />
  <PaymentLinkSheet v-model:open="linking" :tenant-id="tenantId" invoice="" :customer="linkFor" />
  <CustomerQuotasSheet v-model:open="quotasOpen" :tenant-id="tenantId" :customer="quotasOf" />
  <CustomerEditSheets v-model:contact="editingContact" v-model:address="editingAddress" v-model:tax="editingTax" :tenant-id="tenantId" />
  <CreateSubscriptionForm v-model:open="subscribing" :tenant-id="tenantId" :packages="packages.data.value ?? []" :act="act" :for-customer="subscribeFor" />

  <Card class="gap-0 p-3">
    <BulkBar :count="table.selectedRows.value.length" @clear="table.clearSelection()">
      <Button type="button" size="sm" variant="outline" data-testid="bulk-block" @click="bulkBlock(true)">{{ t("billing.customers.block.action") }}</Button>
      <Button type="button" size="sm" variant="outline" data-testid="bulk-unblock" @click="bulkBlock(false)">{{ t("billing.customers.block.lift") }}</Button>
      <Button type="button" size="sm" variant="outline" data-testid="bulk-export" @click="exportSelected">{{ t("billing.customers.exportSelected") }}</Button>
    </BulkBar>
    <p v-if="bulkNote" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ bulkNote }}</p>
    <h2 class="sr-only">{{ t("shell.items.customers") }}</h2>
    <ErrorState :error="customers.error.value" :retrying="customers.isFetching.value" compact @retry="customers.refetch()" />
    <ErrorText :error="act.create.error.value" class="px-3 pb-2" />
    <DataToolbar v-if="customers.isPending.value || list.length > 0 || customers.error.value" v-model:search="table.search.value" :placeholder="t('billing.customers.searchPlaceholder')" :active="active" @clear="clear">
      <FilterSelect v-model="subscriptionFilter" multiple :label="t('billing.customers.filters.subscription')" :options="subscriptionOptions" />
      <FilterSelect v-model="billingFilter" multiple :label="t('billing.customers.filters.billing')" :options="billingOptions" />
      <FilterSelect v-model="countryFilter" multiple :label="t('billing.customers.country')" :options="countryOptions" />
      <FilterSelect v-model="currencyFilter" multiple :label="t('billing.customers.currency')" :options="currencyOptions" />
      <DateRangePicker v-model="range" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>

    <!-- Nothing yet: say what a customer is and offer the first one. -->
    <div v-if="!customers.isPending.value && list.length === 0 && !customers.error.value" class="flex flex-col items-center gap-3 px-4 py-14 text-center" data-testid="customers-empty">
      <span class="bg-brand-soft text-brand-strong inline-flex size-12 items-center justify-center rounded-2xl"><Users class="size-6" aria-hidden="true" /></span>
      <p class="m-0 text-base font-semibold">{{ t("billing.customers.empty") }}</p>
      <p class="text-muted-foreground m-0 max-w-sm text-sm">{{ t("billing.customers.emptyHint") }}</p>
    </div>

    <template v-else>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
            <TableHead>{{ t("billing.customers.columns.customer") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("billing.customers.columns.subscriptions") }}</TableHead>
            <TableHead class="max-xl:hidden">{{ t("billing.customers.columns.currency") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("billing.customers.country") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.customers.columns.spend") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("billing.customers.columns.created") }}</TableHead>
            <TableHead class="max-xl:hidden">{{ t("billing.customers.columns.lastPayment") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="customers.isPending.value">
            <TableRow v-for="n in 6" :key="n" class="hover:bg-transparent" data-testid="customers-skeleton">
              <TableCell />
              <TableCell>
                <div class="flex items-center gap-3"><Skeleton class="size-9 rounded-full" /><div class="flex flex-col gap-1.5"><Skeleton class="h-3.5 w-32" /><Skeleton class="h-3 w-44 max-sm:w-28" /></div></div>
              </TableCell>
              <TableCell class="max-md:hidden"><Skeleton class="h-6 w-20 rounded-full" /></TableCell>
              <TableCell class="max-xl:hidden"><Skeleton class="h-3.5 w-10" /></TableCell>
              <TableCell class="max-lg:hidden"><Skeleton class="h-3.5 w-16" /></TableCell>
              <TableCell><Skeleton class="ms-auto h-3.5 w-16" /></TableCell>
              <TableCell class="max-lg:hidden"><Skeleton class="h-3.5 w-20" /></TableCell>
              <TableCell class="max-xl:hidden"><Skeleton class="h-3.5 w-20" /></TableCell>
              <TableCell />
            </TableRow>
          </template>
          <TableRow
            v-for="(c, i) in table.items.value"
            :key="c.id"
            class="cursor-pointer"
            :data-state="table.isOpen(i) ? 'selected' : undefined"
            :data-testid="`customer-${c.external_id}`"
            @click="open(i)"
          >
            <TableCell><RowCheck :label="`${t('common.bulk.selectRow')} ${c.external_id}`" :checked="table.isTicked(c)" @change="(on) => table.tick(c, on)" /></TableCell>
            <TableCell class="w-full max-w-0 min-w-0">
              <div class="flex min-w-0 items-center gap-3">
                <CustomerAvatar :customer="c" />
                <div class="flex min-w-0 flex-col">
                  <span class="flex min-w-0 items-center gap-2"><span class="truncate font-medium" dir="auto">{{ c.name || c.external_id }}</span><StatusPill v-if="c.block_time" tone="danger" :title="c.block_reason || undefined" data-testid="customer-blocked">{{ t("billing.customers.block.badge") }}</StatusPill></span>
                  <span class="text-muted-foreground truncate text-xs" dir="auto">{{ maskEmail(c.email) || c.external_id }}</span>
                </div>
              </div>
            </TableCell>
            <TableCell class="max-md:hidden">
              <Skeleton v-if="summaries.loading.value" class="h-6 w-20 rounded-full" />
              <div v-else-if="summaryOf(c).subscriptionStatus !== 'none' || summaryOf(c).openInvoices > 0" class="flex flex-col items-start gap-1">
                <div class="flex flex-wrap items-center gap-1">
                  <StatusPill v-if="summaryOf(c).subscriptionStatus !== 'none'" :tone="STATUS_TONES[summaryOf(c).subscriptionStatus]">{{ t(`billing.customers.subscriptionStatus.${summaryOf(c).subscriptionStatus}`) }}</StatusPill>
                  <StatusPill v-if="summaryOf(c).openInvoices > 0" tone="warning">{{ t("billing.customers.stats.openInvoices", { count: summaryOf(c).openInvoices }) }}</StatusPill>
                </div>
                <span v-if="summaryOf(c).plans.length" class="text-muted-foreground mono max-w-48 truncate text-xs">{{ summaryOf(c).plans.join(", ") }}</span>
              </div>
              <span v-else class="text-muted-foreground">—</span>
            </TableCell>
            <TableCell class="mono max-xl:hidden">{{ c.billing_currency || "—" }}</TableCell>
            <TableCell class="max-lg:hidden">
              <span v-if="c.country" class="inline-flex items-center gap-1.5 whitespace-nowrap" :title="countryName(c.country, locale)"><span aria-hidden="true">{{ flagOf(c.country) }}</span>{{ c.country }}</span>
              <span v-else class="text-muted-foreground">—</span>
            </TableCell>
            <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">
              <Skeleton v-if="summaries.loading.value" class="ms-auto h-3.5 w-16" />
              <template v-else-if="spendOf(c)">
                {{ formatMinor(spendOf(c)?.minor, spendOf(c)?.currency, locale) }}<span v-if="spendOf(c)?.more" class="text-muted-foreground ms-1 text-xs font-normal">+{{ spendOf(c)?.more }}</span>
              </template>
              <span v-else class="text-muted-foreground font-normal">—</span>
            </TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-lg:hidden">{{ fmt.date(c.create_time, "day", "—") }}</TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap max-xl:hidden">{{ summaries.loading.value ? "" : fmt.date(summaryOf(c).lastPayment, "day", "—") }}</TableCell>
            <TableCell class="text-end"><RowActions :actions="actionsOf(c, i)" :label="`${t('common.moreActions')} ${c.external_id}`" /></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-6 text-center text-sm">{{ t("common.noMatches") }}</p>
      <DataPagination v-if="list.length > 0" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
      <p v-if="summaries.partial.value" class="text-muted-foreground px-3 pt-2 text-xs">{{ t("billing.customers.partialFigures") }}</p>
    </template>
  </Card>
</template>
