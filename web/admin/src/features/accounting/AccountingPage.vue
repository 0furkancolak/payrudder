<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useApps } from "@/features/apps/api";
import { useTenant } from "@/features/tenants";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { downloadCsv, useAccountingSummary, useExpenses, type Expense } from "./api";
import BarSeries from "./BarSeries.vue";
import DonutChart from "./DonutChart.vue";
import ExpenseDialog from "./ExpenseDialog.vue";
import { granularityOf, PRESETS, presetRange, validRange, type Preset, type Range } from "./period";
import VoidDialog from "./VoidDialog.vue";

const { t, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);

// --- the period and the app
const preset = ref<Preset>("thisMonth");
const custom = ref<Range>({ from: "", to: "" });
const range = computed(() => presetRange(preset.value, new Date(), custom.value));
const rangeOk = computed(() => validRange(range.value));
const granularity = computed(() => (rangeOk.value ? granularityOf(range.value) : "day"));
const app = ref("");
const accountId = computed(() => tenant.data.value?.account_id ?? "");
const apps = useApps(accountId);
const appList = computed(() => (apps.data.value?.apps ?? []).map((a) => ({ id: a.id ?? "", name: a.name ?? a.id ?? "" })).filter((a) => a.id !== ""));
const appName = (id?: string) => appList.value.find((a) => a.id === id)?.name ?? id ?? "";

const summary = useAccountingSummary(tenantId, () => range.value.from, () => range.value.to, app, granularity, rangeOk);
const currencies = computed(() => summary.data.value?.currencies ?? []);
const money = (v: string | number | undefined, currency?: string) => formatMinor(v === undefined ? "0" : String(v), currency, locale.value);

// --- the expenses
const showVoided = ref(false);
const expenses = useExpenses(tenantId, app, showVoided);
const day = (iso?: string) => (iso ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${iso}T00:00:00Z`)) : "");

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

const exporting = ref(false);
async function exportCsv() {
  exporting.value = true;
  try {
    await downloadCsv(tenantId.value, range.value.from, range.value.to, app.value, granularity.value);
  } catch (e) {
    toast.error(errorText(e));
  } finally {
    exporting.value = false;
  }
}
</script>

<template>
  <Card class="mb-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold">{{ t("billing.accounting.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("billing.accounting.intro") }}</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="!rangeOk || exporting" @click="exportCsv">{{ t("billing.accounting.export") }}</Button>
        <Button type="button" @click="add">{{ t("billing.accounting.addExpense") }}</Button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.accounting.period.label')">
      <Button v-for="p in PRESETS" :key="p" type="button" size="sm" :variant="preset === p ? 'default' : 'outline'" :aria-pressed="preset === p" @click="preset = p">
        {{ t(`billing.accounting.period.${p}`) }}
      </Button>
      <template v-if="preset === 'custom'">
        <Input v-model="custom.from" type="date" class="w-40" :aria-label="t('billing.accounting.period.from')" />
        <Input v-model="custom.to" type="date" class="w-40" :aria-label="t('billing.accounting.period.to')" />
      </template>
      <NativeSelect v-if="appList.length" v-model="app" class="w-48" :aria-label="t('billing.accounting.app.label')">
        <option value="">{{ t("billing.accounting.app.all") }}</option>
        <option v-for="a in appList" :key="a.id" :value="a.id">{{ a.name }}</option>
      </NativeSelect>
    </div>
    <p v-if="preset === 'custom' && !rangeOk" class="text-muted-foreground text-sm">{{ t("billing.accounting.period.invalid") }}</p>
    <p v-if="app" class="text-muted-foreground text-xs">{{ t("billing.accounting.app.note") }}</p>
    <p class="text-muted-foreground text-xs">{{ t("billing.accounting.currencyNote") }}</p>
  </Card>

  <p v-if="summary.error.value" class="text-destructive mb-4 text-sm" role="alert">{{ errorText(summary.error.value) }}</p>
  <p v-if="rangeOk && summary.data.value && !currencies.length" class="text-muted-foreground mb-6 text-sm">{{ t("billing.accounting.empty") }}</p>

  <section v-for="c in currencies" :key="c.currency" class="mb-6" :aria-label="c.currency">
    <Card>
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <h3 class="text-lg font-semibold">{{ c.currency }}</h3>
        <span class="text-muted-foreground text-sm">{{ t("billing.accounting.paidInvoices", { count: Number(c.paid_invoices ?? 0) }) }}</span>
      </div>
      <dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div data-stat="income">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.income") }}</dt>
          <dd class="text-xl font-semibold tabular-nums">{{ money(c.gross_income_minor, c.currency) }}</dd>
        </div>
        <div data-stat="tax">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.tax") }}</dt>
          <dd class="text-xl font-semibold tabular-nums">{{ money(c.tax_minor, c.currency) }}</dd>
        </div>
        <div data-stat="refunds">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.refunds") }}</dt>
          <dd class="text-xl font-semibold tabular-nums">{{ money(c.refunds_minor, c.currency) }}</dd>
        </div>
        <div data-stat="net">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.net") }}</dt>
          <dd class="text-xl font-semibold tabular-nums">{{ money(c.net_income_minor, c.currency) }}</dd>
        </div>
        <div data-stat="fees">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.fees") }}</dt>
          <dd v-if="c.provider_fees_recorded" class="text-xl font-semibold tabular-nums">{{ money(c.provider_fees_minor, c.currency) }}</dd>
          <dd v-else class="text-muted-foreground text-xl" :title="t('billing.accounting.stat.feesHint')">{{ t("billing.accounting.stat.feesNotRecorded") }}</dd>
        </div>
        <div data-stat="expenses">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.expenses") }}</dt>
          <dd class="text-xl font-semibold tabular-nums">{{ money(c.expenses_minor, c.currency) }}</dd>
        </div>
        <div data-stat="profit" class="sm:col-span-2">
          <dt class="text-muted-foreground text-sm">{{ t("billing.accounting.stat.profit") }}</dt>
          <dd class="text-2xl font-semibold tabular-nums" :class="(c.profit_minor ?? '').startsWith('-') ? 'text-destructive' : 'text-success'">{{ money(c.profit_minor, c.currency) }}</dd>
        </div>
      </dl>

      <div class="grid gap-6 lg:grid-cols-2">
        <div>
          <h4 class="mb-2 text-sm font-medium">{{ t("billing.accounting.donut.title") }}</h4>
          <DonutChart v-if="(c.expense_by_category ?? []).length" :items="c.expense_by_category ?? []" :currency="c.currency ?? ''" />
          <p v-else class="text-muted-foreground text-sm">{{ t("billing.accounting.donut.none") }}</p>
        </div>
        <div>
          <h4 class="mb-2 text-sm font-medium">{{ t("billing.accounting.bars.title") }}</h4>
          <BarSeries :points="c.series ?? []" :currency="c.currency ?? ''" :granularity="granularity" />
        </div>
      </div>
    </Card>
  </section>

  <Card class="p-3">
    <div class="flex flex-wrap items-center justify-between gap-2 px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.accounting.expenses.title") }}</h2>
      <label class="text-muted-foreground flex items-center gap-2 text-sm">
        <Checkbox :model-value="showVoided" @update:model-value="showVoided = $event === true" />
        {{ t("billing.accounting.expenses.showVoided") }}
      </label>
    </div>
    <p v-if="expenses.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(expenses.error.value) }}</p>
    <p v-if="expenses.data.value && !(expenses.data.value.expenses ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.accounting.expenses.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.accounting.form.date") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.vendor") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.category") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.accounting.form.amount") }}</TableHead>
          <TableHead>{{ t("billing.accounting.form.recurring") }}</TableHead>
          <TableHead v-if="appList.length">{{ t("billing.accounting.form.app") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in expenses.data.value?.expenses ?? []" :key="e.id" :class="e.void_time ? 'text-muted-foreground' : ''">
          <TableCell>{{ day(e.incurred_on) }}</TableCell>
          <TableCell class="font-medium">
            {{ e.vendor }}
            <Badge v-if="e.void_time" variant="secondary" class="ms-2" :title="e.void_reason">{{ t("billing.accounting.expenses.voided") }}</Badge>
          </TableCell>
          <TableCell>{{ t(`billing.accounting.category.${e.category}`) }}</TableCell>
          <TableCell class="text-end tabular-nums" :class="e.void_time ? 'line-through' : ''">{{ money(e.amount_minor, e.currency) }}</TableCell>
          <TableCell>
            <template v-if="e.recurring && e.recurring !== 'none'">
              {{ t(`billing.accounting.recurring.${e.recurring}`) }}<span v-if="e.recurring_until"> · {{ t("billing.accounting.expenses.until", { date: day(e.recurring_until) }) }}</span>
            </template>
            <template v-else>{{ t("billing.accounting.recurring.none") }}</template>
          </TableCell>
          <TableCell v-if="appList.length">{{ e.app_id ? appName(e.app_id) : t("billing.accounting.form.wholeTenant") }}</TableCell>
          <TableCell class="text-end whitespace-nowrap">
            <template v-if="!e.void_time">
              <Button type="button" variant="outline" size="sm" class="me-2" @click="edit(e)">{{ t("billing.accounting.expenses.edit") }}</Button>
              <Button type="button" variant="outline" size="sm" @click="voidOne(e)">{{ t("billing.accounting.expenses.void") }}</Button>
            </template>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>

  <ExpenseDialog v-model:open="dialogOpen" :tenant-id="tenantId" :expense="editing" :default-currency="tenant.data.value?.currency ?? 'USD'" :apps="appList" />
  <VoidDialog v-model:open="voidOpen" :tenant-id="tenantId" :expense="voiding" />
</template>
