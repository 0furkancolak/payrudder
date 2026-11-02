<script setup lang="ts">
import { ArrowUpRight, Pencil, Plus, Trash2 } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useTenant } from "@/features/tenants/api";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { nameAsc, nameDesc, newest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { RowActions } from "@/shared/ui/row-actions";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { SortSelect } from "@/shared/ui/sort-select";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDeleteFeeRule, useFeeRules, usePutFeeRule } from "./api";
import { draftBody, draftProblem, fixedText, rateLabel, rateText, ruleId, type RuleDraft } from "./feerules";
import { environmentOf } from "./reconcile";

/**
 * What each provider is expected to keep of a payment, per currency: a rate and a fixed part, entered by an operator because Payrudder knows no provider's prices.
 * The settlement list above compares the fee a provider reported with what the rule here says, and a fee that is off by more than a minor unit is a discrepancy
 * in the review queue (docs/reconciliation.md#fee-rules). A rule is named by its provider (or account key) and currency; saving one of those again edits it.
 */
const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const tenant = useTenant(() => props.tenantId);
const environment = computed(() => environmentOf(tenant.data.value?.environment));
const rules = useFeeRules(() => props.tenantId);
type Rule = NonNullable<NonNullable<typeof rules.data.value>["rules"]>[number];
const rows = computed(() => rules.data.value?.rules ?? []);
const money = (minor: number | undefined, currency: string | undefined) => formatMinor(String(minor ?? 0), currency, locale.value);
/** A provider's name in the reader's language; for an account key ("stripe~eu") the name of the provider it is an account of. */
const providerName = (key: string) => label(t, te, "provider", key.split("~")[0]);

// --- the list: search, two filters, one sort, a detail sheet per rule
const all = computed(() => ({ value: "", label: t("common.all") }));
const distinct = (pick: (r: Rule) => string) => [...new Set(rows.value.map(pick).filter(Boolean))].sort();
const table = useTableState<Rule>(rows, {
  searchFields: (r) => [r.provider, r.currency],
  sorts: () => [nameAsc(t, (r) => `${r.provider} ${r.currency}`), nameDesc(t, (r) => `${r.provider} ${r.currency}`), newest(t, (r) => time(r.updated_at))],
  filters: { provider: (r, v) => r.provider === v, currency: (r, v) => r.currency === v },
  url: "fee-",
  sheet: { kind: "fee-rule", id: ruleId, loaded: () => !rules.isPending.value },
});
const providerFilter = computed({ get: () => table.filters.value.provider ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, provider: v }) });
const currencyFilter = computed({ get: () => table.filters.value.currency ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, currency: v }) });
const providerOptions = computed(() => [all.value, ...distinct((r) => r.provider).map((p) => ({ value: p, label: label(t, te, "provider", p) }))]);
const currencyOptions = computed(() => [all.value, ...distinct((r) => r.currency).map((c) => ({ value: c, label: c }))]);
const current = table.row;
const detailRows = computed(() => {
  const r = current.value;
  if (!r) return [];
  return [
    { key: "provider", label: t("billing.feeRules.provider"), value: providerName(r.provider) },
    { key: "account", label: t("billing.feeRules.account"), value: r.provider, mono: true },
    { key: "currency", label: t("billing.feeRules.currency"), value: r.currency },
    { key: "rate", label: t("billing.feeRules.rate"), value: rateLabel(r.rate_bps, locale.value) },
    { key: "fixed", label: t("billing.feeRules.fixed"), value: money(r.fixed_minor, r.currency) },
    { key: "environment", label: t("billing.feeRules.environment"), value: label(t, te, "environment", r.environment) },
    { key: "updated", label: t("billing.feeRules.updated"), value: fmt.date(r.updated_at, "short") },
  ];
});

// --- the form: a new rule, or the one being edited (its provider and currency are its name and stay)
const put = usePutFeeRule(() => props.tenantId);
const del = useDeleteFeeRule(() => props.tenantId);
const formOpen = ref(false);
const editing = ref<Rule | null>(null);
const draft = ref<RuleDraft>({ provider: "", currency: "", rate: "", fixed: "" });
const problem = computed(() => draftProblem(draft.value));
const touched = ref(false);
function openForm(rule: Rule | null) {
  editing.value = rule;
  draft.value = rule ? { provider: rule.provider, currency: rule.currency, rate: rateText(rule.rate_bps), fixed: fixedText(rule.fixed_minor, rule.currency) } : { provider: "", currency: "", rate: "", fixed: "" };
  touched.value = false;
  put.reset();
  formOpen.value = true;
}
function save() {
  touched.value = true;
  if (problem.value || !environment.value) return;
  put.mutate({ environment: editing.value?.environment ?? environment.value, ...draftBody(draft.value) }, { onSuccess: () => (formOpen.value = false) });
}
async function remove(r: Rule) {
  if (!(await askConfirm({ body: t("billing.feeRules.confirmRemove", { provider: r.provider, currency: r.currency }) }))) return;
  del.mutate({ environment: r.environment, provider: r.provider, currency: r.currency }, { onSuccess: () => (table.detail.value = null) });
}
const rowActions = (r: Rule, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) },
  { key: "edit", label: t("billing.feeRules.edit"), icon: Pencil, onSelect: () => openForm(r) },
  { key: "remove", label: t("billing.feeRules.remove"), icon: Trash2, destructive: true, onSelect: () => void remove(r) },
];
</script>

<template>
  <Card class="mb-6" data-testid="fee-rules-card">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold">{{ t("billing.feeRules.title") }}</h2>
        <p class="text-muted-foreground max-w-2xl text-sm">{{ t("billing.feeRules.hint") }}</p>
      </div>
      <Button size="sm" variant="outline" :disabled="!environment" data-testid="fee-rule-add" @click="openForm(null)"><Plus aria-hidden="true" />{{ t("billing.feeRules.add") }}</Button>
    </div>
    <ErrorText :error="rules.error.value ?? del.error.value" />
    <div class="-mx-3 border-t pt-3">
      <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
        <FilterSelect v-model="providerFilter" multiple :label="t('billing.feeRules.provider')" :options="providerOptions" />
        <FilterSelect v-model="currencyFilter" multiple :label="t('billing.feeRules.currency')" :options="currencyOptions" />
        <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      </DataToolbar>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.feeRules.provider") }}</TableHead>
            <TableHead>{{ t("billing.feeRules.currency") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.feeRules.rate") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.feeRules.fixed") }}</TableHead>
            <TableHead>{{ t("billing.feeRules.updated") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(r, i) in table.items.value" :key="ruleId(r)" class="cursor-pointer" :data-testid="`fee-rule-${r.provider}-${r.currency}`" @click="table.open(i)">
            <TableCell class="font-medium">{{ providerName(r.provider) }} <span v-if="r.provider.includes('~')" class="mono text-muted-foreground text-xs">{{ r.provider }}</span></TableCell>
            <TableCell>{{ r.currency }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ rateLabel(r.rate_bps, locale) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ money(r.fixed_minor, r.currency) }}</TableCell>
            <TableCell class="whitespace-nowrap">{{ fmt.date(r.updated_at, "short") }}</TableCell>
            <TableCell><RowActions :actions="rowActions(r, i)" /></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableEmpty v-if="!rules.isPending.value && rows.length === 0 && !rules.error.value">{{ t("billing.feeRules.empty") }}</TableEmpty>
      <TableEmpty v-else-if="table.total.value === 0 && table.active.value">{{ t("common.noMatches") }}</TableEmpty>
      <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
    </div>
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current ? `${current.provider.includes('~') ? current.provider : providerName(current.provider)} · ${current.currency}` : ''" :description="t('billing.feeRules.detailHint')">
    <DetailList :rows="detailRows" />
    <template #footer>
      <Button v-if="current" variant="outline" data-testid="fee-rule-edit" @click="openForm(current)"><Pencil aria-hidden="true" />{{ t("billing.feeRules.edit") }}</Button>
      <Button v-if="current" variant="outline" class="text-destructive" data-testid="fee-rule-remove" @click="remove(current)"><Trash2 aria-hidden="true" />{{ t("billing.feeRules.remove") }}</Button>
    </template>
  </DetailSheet>

  <Sheet v-model:open="formOpen" name="fee-rule-form">
    <SheetContent class="sm:max-w-md" data-testid="fee-rule-sheet">
      <SheetHeader>
        <SheetTitle>{{ editing ? t("billing.feeRules.editTitle", { provider: editing.provider, currency: editing.currency }) : t("billing.feeRules.add") }}</SheetTitle>
        <SheetDescription>{{ t("billing.feeRules.formHint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="fee-rule-form" class="grid gap-4" @submit.prevent="save">
          <FormField :label="t('billing.feeRules.account')" for="fr-provider" :hint="t('billing.feeRules.accountHint')">
            <Input id="fr-provider" v-model="draft.provider" class="mono" placeholder="stripe" :disabled="!!editing" autocapitalize="off" data-testid="fee-rule-provider" />
          </FormField>
          <FormField :label="t('billing.feeRules.currency')" for="fr-currency" :hint="t('billing.feeRules.currencyHint')">
            <Input id="fr-currency" v-model="draft.currency" class="mono uppercase" maxlength="3" placeholder="USD" :disabled="!!editing" data-testid="fee-rule-currency" />
          </FormField>
          <FormField :label="t('billing.feeRules.rateInput')" for="fr-rate" :hint="t('billing.feeRules.rateHint')">
            <Input id="fr-rate" v-model="draft.rate" inputmode="decimal" placeholder="2.9" data-testid="fee-rule-rate" />
          </FormField>
          <FormField :label="t('billing.feeRules.fixedInput')" for="fr-fixed" :hint="t('billing.feeRules.fixedHint')">
            <Input id="fr-fixed" v-model="draft.fixed" inputmode="decimal" placeholder="0.30" data-testid="fee-rule-fixed" />
          </FormField>
          <p v-if="touched && problem" class="text-destructive text-sm" role="alert" data-testid="fee-rule-problem">{{ t(`billing.feeRules.errors.${problem}`) }}</p>
          <ErrorText :error="put.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="formOpen = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="fee-rule-form" :disabled="put.isPending.value || !environment" data-testid="fee-rule-save">{{ t("billing.feeRules.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
