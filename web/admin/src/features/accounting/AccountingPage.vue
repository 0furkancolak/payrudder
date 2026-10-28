<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";

import { Download, Pencil, Ban, ScanLine, Paperclip } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { toast } from "vue-sonner";
import { useShell } from "@/app/layout/useShell";
import { useAccountApps } from "@/features/apps/api";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import { ALL_APPS } from "@/shared/lib/scope";
import { useTenant } from "@/features/tenants";
import { useErrorText } from "@/shared/lib/errors";
import { formatUtcDay } from "@/shared/lib/format";
import { ErrorState } from "@/shared/ui/error-state";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { inRange, thisMonth } from "@/shared/lib/dateRange";
import { names } from "@/shared/lib/sheets";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { CATEGORIES, downloadCsv, receiptUrl, useAccountingSummary, useExpenseRules, useExpenses, useInboxItems, type Expense, type InboxItem } from "./api";
import CurrencySummaryCard from "./CurrencySummaryCard.vue";
import ExpenseDialog from "./ExpenseDialog.vue";
import ExpenseRulesSheet from "./ExpenseRulesSheet.vue";
import InboxCard from "./InboxCard.vue";
import ReceiptScanSheet from "./ReceiptScanSheet.vue";
import { granularityOf, presetRange, validRange } from "./period";
import VoidDialog from "./VoidDialog.vue";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

const { t, locale } = useI18n();
const fmt = useFormat();
const errorText = useErrorText();
const tenantId = useRouteTenantId();
const tenant = useTenant(tenantId);

// --- the period and the app
const range = ref<DateRange>(presetRange("thisMonth", new Date()));
const rangeOk = computed(() => validRange(range.value));
const granularity = computed(() => (rangeOk.value ? granularityOf(range.value) : "day"));
const app = ref("");
// The page follows the scope in the address: one app shows that app's expenses, all apps show them all.
const shell = useShell();
watch(() => shell.scope.value, (sc) => (app.value = sc && sc !== ALL_APPS ? sc : ""), { immediate: true });
const accountId = computed(() => tenant.data.value?.account_id ?? "");
const apps = useAccountApps(accountId);
const appList = computed(() => (apps.data.value?.apps ?? []).map((a) => ({ id: a.id ?? "", name: a.name ?? a.id ?? "" })).filter((a) => a.id !== ""));
const appName = (id?: string) => appList.value.find((a) => a.id === id)?.name ?? id ?? "";

const summary = useAccountingSummary(tenantId, () => range.value.from, () => range.value.to, app, granularity, rangeOk);
const currencies = computed(() => summary.data.value?.currencies ?? []);
const money = (v: string | number | undefined, currency?: string) => fmt.amount(v, currency);
const defaultCurrency = computed(() => tenant.data.value?.currency || FALLBACK_CURRENCY);

// --- the expenses
const showVoided = ref(false);
const expenses = useExpenses(tenantId, app, showVoided);
const expenseRange = ref<DateRange>(thisMonth());
const table = useTableState(() => (expenses.data.value?.expenses ?? []).filter((e) => inRange(e.incurred_on, expenseRange.value)), {
  sheet: { kind: "expense", id: (e) => e.id }, searchFields: (e) => [e.vendor, e.note, e.category ? t(`billing.accounting.category.${e.category}`) : "", e.amount_minor],
  url: true,
  filters: {
    category: (e, v) => e.category === v,
    recurring: (e, v) => (e.recurring ?? "none") === v,
  },
});
const filter = (key: string) => computed({ get: () => table.filters.value[key] ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const categoryFilter = filter("category");
const recurringFilter = filter("recurring");
watch(expenseRange, () => (table.page.value = 1), { deep: true });
const expensesActive = computed(() => table.active.value || expenseRange.value.from !== "" || expenseRange.value.to !== "");
function clearExpenses() {
  table.clear();
  expenseRange.value = { from: "", to: "" };
}
const appOptions = computed(() => [{ value: "", label: t("billing.accounting.app.all") }, ...appList.value.map((a) => ({ value: a.id, label: a.name }))]);
const categoryOptions = computed(() => [{ value: "", label: t("billing.accounting.expenses.allCategories") }, ...CATEGORIES.map((c) => ({ value: c, label: t(`billing.accounting.category.${c}`) }))]);
const recurringOptions = computed(() => [{ value: "", label: t("billing.accounting.expenses.allRecurring") }, ...["none", "monthly", "yearly"].map((r) => ({ value: r, label: t(`billing.accounting.recurring.${r}`) }))]);
const current = table.row;
const actionsOf = (e: Expense, i: number) =>
  e.void_time
    ? [{ key: "view", label: t("common.view"), onSelect: () => table.open(i) }]
    : [
        { key: "edit", label: t("billing.accounting.expenses.edit"), icon: Pencil, onSelect: () => edit(e) },
        { key: "void", label: t("billing.accounting.expenses.void"), icon: Ban, destructive: true, separatorBefore: true, onSelect: () => voidOne(e) },
      ];
const day = (iso?: string) => formatUtcDay(iso, locale.value);

const recurringText = (e: Expense) =>
  e.recurring && e.recurring !== "none"
    ? `${t(`billing.accounting.recurring.${e.recurring}`)}${e.recurring_until ? ` · ${t("billing.accounting.expenses.until", { date: day(e.recurring_until) })}` : ""}`
    : t("billing.accounting.recurring.none");

/** Closes the detail sheet, then runs an action on the expense it showed (its dialog opens on its own). */
function from(act: (e: Expense) => void, e: Expense | undefined) {
  table.detail.value = null;
  if (e) act(e);
}

// --- scanning receipts and the invoice inbox
const scanOpen = ref(false);
const scanInbox = ref<InboxItem[] | undefined>(undefined);
const rulesOpen = ref(false);
const rules = useExpenseRules(tenantId);
const inboxItems = useInboxItems(tenantId);
function scan() {
  scanInbox.value = undefined;
  scanOpen.value = true;
}
function reviewInbox() {
  scanInbox.value = inboxItems.data.value?.items ?? [];
  scanOpen.value = true;
}
async function openReceipt(id?: string) {
  if (!id) return;
  try {
    const url = await receiptUrl(tenantId.value, id);
    if (url) window.open(url, "_blank", "noopener");
  } catch (e) {
    toast.error(errorText(e));
  }
}

const dialogOpen = ref(false);
const editing = ref<Expense | null>(null);
const voidOpen = ref(false);
const voiding = ref<Expense | null>(null);
function add() {
  editing.value = null;
  dialogOpen.value = true;
}
function edit(e: Expense) {
  editing.value = e;
  dialogOpen.value = true;
}
function voidOne(e: Expense) {
  voiding.value = e;
  voidOpen.value = true;
}
// Editing and voiding an expense are in the address with its id (a new expense, the scan and the rules have names of their own): a reload or a shared link opens the form once the expenses have loaded.
const expenseOf = (id: string) => expenses.data.value?.expenses?.find((e) => e.id === id);
useSheetTarget("edit-expense", dialogOpen, editing, { id: (e) => e.id ?? "", find: expenseOf });
useSheetTarget("void-expense", voidOpen, voiding, { id: (e) => e.id ?? "", find: expenseOf });
// The inbox review is the scan sheet with the inbox's items; the address holds only that it is open, so the items are read again.
watch(
  [() => names.value.includes("review-inbox"), () => inboxItems.data.value],
  ([named, data]) => {
    if (named && data && !scanInbox.value) scanInbox.value = data.items ?? [];
  },
  { immediate: true },
);

const exporting = ref(false);
async function exportCsv(report: "export" | "coupons" = "export") {
  exporting.value = true;
  try {
    await downloadCsv(tenantId.value, range.value.from, range.value.to, app.value, granularity.value, report);
  } catch (e) {
    toast.error(errorText(e));
  } finally {
    exporting.value = false;
  }
}
</script>

<template>
  <div class="mb-6 flex flex-col gap-2">
    <div class="flex flex-wrap items-center gap-2" role="search" :aria-label="t('billing.accounting.period.label')">
      <DateRangePicker v-model="range" />
      <FilterSelect v-if="appList.length" v-model="app" :label="t('billing.accounting.app.label')" :options="appOptions" />
      <div class="ms-auto flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" :disabled="!rangeOk || exporting" @click="exportCsv('export')"><Download class="size-4" aria-hidden="true" />{{ t("billing.accounting.export") }}</Button>
        <Button type="button" variant="outline" data-testid="scan-receipts" @click="scan"><ScanLine class="size-4" aria-hidden="true" />{{ t("billing.accounting.scan.open") }}</Button>
        <HeaderAction test-id="add-expense" :label="t('billing.accounting.addExpense')" @click="add" />
      </div>
    </div>
    <p v-if="!rangeOk" class="text-muted-foreground text-sm">{{ t("billing.accounting.period.invalid") }}</p>
    <p class="text-muted-foreground text-xs">{{ t("billing.accounting.intro") }}</p>
    <p v-if="app" class="text-muted-foreground text-xs">{{ t("billing.accounting.app.note") }}</p>
    <p class="text-muted-foreground text-xs">{{ t("billing.accounting.currencyNote") }}</p>
  </div>

  <InboxCard :tenant-id="tenantId" @review="reviewInbox" @rules="rulesOpen = true" />

  <ErrorState :error="summary.error.value" :retrying="summary.isFetching.value" class="mb-4" @retry="summary.refetch()" />
  <p v-if="rangeOk && summary.data.value && !currencies.length" class="text-muted-foreground mb-6 text-sm">{{ t("billing.accounting.empty") }}</p>

  <CurrencySummaryCard v-for="c in currencies" :key="c.currency" :summary="c" :granularity="granularity" :can-export="rangeOk && !exporting" @export-coupons="exportCsv('coupons')" />

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.accounting.expenses.title") }}</h2>
    <ErrorState :error="expenses.error.value" :retrying="expenses.isFetching.value" compact @retry="expenses.refetch()" />
    <DataToolbar v-model:search="table.search.value" :active="expensesActive" class="pt-3" @clear="clearExpenses">
      <FilterSelect v-model="categoryFilter" multiple :label="t('billing.accounting.form.category')" :options="categoryOptions" />
      <FilterSelect v-model="recurringFilter" multiple :label="t('billing.accounting.form.recurring')" :options="recurringOptions" />
      <DateRangePicker v-model="expenseRange" />
      <label class="text-muted-foreground flex items-center gap-2 text-sm">
        <Checkbox :model-value="showVoided" @update:model-value="showVoided = $event === true" />
        {{ t("billing.accounting.expenses.showVoided") }}
      </label>
    </DataToolbar>
    <p v-if="expenses.data.value && !(expenses.data.value.expenses ?? []).length" class="text-muted-foreground px-3 py-4 text-center text-sm">{{ t("billing.accounting.expenses.none") }}</p>
    <p v-else-if="!table.total.value" class="text-muted-foreground px-3 py-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.accounting.form.date") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.vendor") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.category") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.accounting.form.amount") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.recurring") }}</TableHead>
          <TableHead v-if="appList.length">{{ t("billing.accounting.form.app") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(e, i) in table.items.value" :key="e.id" class="cursor-pointer" :class="e.void_time ? 'text-muted-foreground' : ''" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
          <TableCell>{{ day(e.incurred_on) }}</TableCell>
          <TableCell class="font-medium">
            {{ e.vendor }}
            <Badge v-if="e.void_time" variant="secondary" class="ms-2" :title="e.void_reason">{{ t("billing.accounting.expenses.voided") }}</Badge>
          </TableCell>
          <TableCell>{{ t(`billing.accounting.category.${e.category}`) }}</TableCell>
          <TableCell class="text-end tabular-nums" :class="e.void_time ? 'line-through' : ''">{{ money(e.amount_minor, e.currency) }}</TableCell>
          <TableCell>{{ recurringText(e) }}</TableCell>
          <TableCell v-if="appList.length">{{ e.app_id ? appName(e.app_id) : t("billing.accounting.form.wholeTenant") }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="actionsOf(e, i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="table.total.value" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.vendor ?? ''" :description="current ? day(current.incurred_on) : undefined">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="expense-detail">
      <dt class="text-muted-foreground">{{ t("billing.accounting.form.amount") }}</dt>
      <dd class="text-end text-lg font-semibold tabular-nums" :class="current.void_time ? 'line-through' : ''">{{ money(current.amount_minor, current.currency) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.accounting.form.category") }}</dt>
      <dd class="text-end">{{ t(`billing.accounting.category.${current.category}`) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.accounting.form.recurring") }}</dt>
      <dd class="text-end">{{ recurringText(current) }}</dd>
      <template v-if="appList.length">
        <dt class="text-muted-foreground">{{ t("billing.accounting.form.app") }}</dt>
        <dd class="text-end">{{ current.app_id ? appName(current.app_id) : t("billing.accounting.form.wholeTenant") }}</dd>
      </template>
      <template v-if="current.tax_minor && current.tax_minor !== '0'">
        <dt class="text-muted-foreground">{{ t("billing.accounting.scan.tax") }}</dt>
        <dd class="text-end tabular-nums">{{ money(current.tax_minor, current.currency) }}</dd>
      </template>
      <template v-if="current.attachment_asset_id">
        <dt class="text-muted-foreground">{{ t("billing.accounting.scan.attachment") }}</dt>
        <dd class="text-end"><Button type="button" variant="link" size="sm" class="h-auto p-0" data-testid="expense-receipt" @click="openReceipt(current.attachment_asset_id)"><Paperclip class="size-4" aria-hidden="true" />{{ t("billing.accounting.scan.openReceipt") }}</Button></dd>
      </template>
      <template v-if="current.note">
        <dt class="text-muted-foreground">{{ t("billing.accounting.form.note") }}</dt>
        <dd class="text-end">{{ current.note }}</dd>
      </template>
      <template v-if="current.void_time">
        <dt class="text-muted-foreground">{{ t("billing.accounting.expenses.voided") }}</dt>
        <dd class="text-end">{{ current.void_reason || day(current.void_time) }}</dd>
      </template>
    </dl>
    <template v-if="current && !current.void_time" #footer>
      <Button type="button" variant="outline" @click="from(edit, current)"><Pencil class="size-4" aria-hidden="true" />{{ t("billing.accounting.expenses.edit") }}</Button>
      <Button type="button" variant="outline" @click="from(voidOne, current)"><Ban class="size-4" aria-hidden="true" />{{ t("billing.accounting.expenses.void") }}</Button>
    </template>
  </DetailSheet>

  <ExpenseDialog v-model:open="dialogOpen" :tenant-id="tenantId" :expense="editing" :default-currency="defaultCurrency" :apps="appList" :default-app="app" />
  <VoidDialog v-model:open="voidOpen" :tenant-id="tenantId" :expense="voiding" />
  <ReceiptScanSheet v-model:open="scanOpen" :tenant-id="tenantId" :default-currency="defaultCurrency" :apps="appList" :rules="rules.data.value?.rules ?? []" :inbox="scanInbox" />
  <ExpenseRulesSheet v-model:open="rulesOpen" :tenant-id="tenantId" :default-currency="defaultCurrency" :apps="appList" />
</template>
