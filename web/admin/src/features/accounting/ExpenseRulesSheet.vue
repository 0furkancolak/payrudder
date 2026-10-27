<script setup lang="ts">
import { Pencil, Trash2 } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { CURRENCIES } from "@/shared/lib/currencies";
import { minorToMajor, parseMajor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { CATEGORIES, useDeleteRule, useExpenseRules, useSaveRule, type Category, type ExpenseRule } from "./api";
import { useFormat } from "@/shared/lib/format";

/**
 * Recurring-vendor rules. A vendor whose name contains the rule's text gets its category, currency and tax rate when an invoice is read; with automatic approval, an
 * invoice that arrives by mail is recorded without a person when its amount is within the tolerance of the amount the rule expects.
 */
const props = defineProps<{ tenantId: string; defaultCurrency: string; apps: { id: string; name: string }[] }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const fmt = useFormat();
const rules = useExpenseRules(() => props.tenantId);
const save = useSaveRule(() => props.tenantId);
const del = useDeleteRule(() => props.tenantId);
const list = computed(() => rules.data.value?.rules ?? []);

const editing = ref<string>("");
const vendor = ref("");
const category = ref<Category>("software");
const currency = ref("USD");
const taxPercent = ref("");
const expected = ref("");
const tolerance = ref("5");
const auto = ref(false);
const app = ref("");
const formError = ref("");

function reset() {
  editing.value = "";
  vendor.value = "";
  category.value = "software";
  currency.value = props.defaultCurrency || "USD";
  taxPercent.value = "";
  expected.value = "";
  tolerance.value = "5";
  auto.value = false;
  app.value = "";
  formError.value = "";
  save.reset();
}
watch(open, (o) => o && reset());

function edit(r: ExpenseRule) {
  editing.value = r.id ?? "";
  vendor.value = r.vendor_match ?? "";
  category.value = (r.category ?? "software") as Category;
  currency.value = r.currency ?? "USD";
  taxPercent.value = r.tax_bps ? String((r.tax_bps ?? 0) / 100) : "";
  expected.value = r.expected_amount_minor && r.expected_amount_minor !== "0" ? minorToMajor(r.expected_amount_minor, currency.value) : "";
  tolerance.value = String((r.tolerance_bps ?? 0) / 100);
  auto.value = !!r.auto_approve;
  app.value = r.app_id ?? "";
}

/** A percentage the person typed ("20", "7,5") in basis points, or null. */
function bps(text: string): number | null {
  if (text.trim() === "") return 0;
  const n = Number(text.trim().replace(",", "."));
  return Number.isFinite(n) && n >= 0 && n <= 100 ? Math.round(n * 100) : null;
}

function submit() {
  formError.value = "";
  const tax = bps(taxPercent.value);
  const tol = bps(tolerance.value);
  const exp = expected.value.trim() === "" ? "0" : parseMajor(expected.value, currency.value);
  if (vendor.value.trim().length < 2 || tax === null || tol === null || exp === null || (auto.value && exp === "0")) {
    formError.value = auto.value && exp === "0" ? t("billing.accounting.rules.needsExpected") : t("billing.accounting.rules.bad");
    return;
  }
  save.mutate(
    {
      id: editing.value || undefined,
      body: { vendor_match: vendor.value.trim(), category: category.value, currency: currency.value, tax_bps: tax, expected_amount_minor: exp, tolerance_bps: tol, auto_approve: auto.value, app_id: app.value || undefined },
    },
    { onSuccess: reset },
  );
}
function remove(r: ExpenseRule) {
  if (r.id && window.confirm(t("billing.accounting.rules.confirmDelete", { vendor: r.vendor_match ?? "" }))) del.mutate(r.id);
}
</script>

<template>
  <Sheet v-model:open="open" name="expense-rules">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.accounting.rules.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.accounting.rules.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody class="gap-5">
        <ul v-if="list.length" class="flex flex-col gap-2" data-testid="rules-list">
          <li v-for="r in list" :key="r.id" class="border-border/70 flex items-center gap-3 rounded-2xl border p-3">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium">{{ r.vendor_match }}</p>
              <p class="text-muted-foreground truncate text-xs">
                {{ t(`billing.accounting.category.${r.category}`) }} · {{ r.currency }}<template v-if="r.tax_bps"> · {{ t("billing.accounting.rules.taxShort", { percent: (r.tax_bps ?? 0) / 100 }) }}</template>
                <template v-if="r.auto_approve"> · {{ t("billing.accounting.rules.autoShort", { amount: fmt.amount(r.expected_amount_minor, r.currency), tolerance: (r.tolerance_bps ?? 0) / 100 }) }}</template>
              </p>
            </div>
            <Button type="button" variant="ghost" size="icon" class="size-8" :aria-label="t('billing.accounting.expenses.edit')" @click="edit(r)"><Pencil class="size-4" aria-hidden="true" /></Button>
            <Button type="button" variant="ghost" size="icon" class="size-8" :aria-label="t('billing.accounting.rules.delete')" @click="remove(r)"><Trash2 class="size-4" aria-hidden="true" /></Button>
          </li>
        </ul>
        <p v-else-if="!rules.isPending.value && !rules.error.value" class="text-muted-foreground text-sm">{{ t("billing.accounting.rules.none") }}</p>

        <form id="rule-form" class="grid gap-3" @submit.prevent="submit">
          <h3 class="text-sm font-semibold">{{ editing ? t("billing.accounting.rules.edit") : t("billing.accounting.rules.add") }}</h3>
          <div class="grid gap-1.5">
            <Label for="rule-vendor">{{ t("billing.accounting.rules.vendor") }}</Label>
            <Input id="rule-vendor" v-model="vendor" maxlength="120" autocomplete="off" />
            <p class="text-muted-foreground text-xs">{{ t("billing.accounting.rules.vendorHint") }}</p>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="grid gap-1.5">
              <Label for="rule-category">{{ t("billing.accounting.form.category") }}</Label>
              <NativeSelect id="rule-category" v-model="category">
                <option v-for="c in CATEGORIES" :key="c" :value="c">{{ t(`billing.accounting.category.${c}`) }}</option>
              </NativeSelect>
            </div>
            <div class="grid gap-1.5">
              <Label for="rule-currency">{{ t("billing.accounting.form.currency") }}</Label>
              <NativeSelect id="rule-currency" v-model="currency">
                <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
              </NativeSelect>
            </div>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="grid gap-1.5">
              <Label for="rule-tax">{{ t("billing.accounting.rules.tax") }}</Label>
              <Input id="rule-tax" v-model="taxPercent" inputmode="decimal" autocomplete="off" />
            </div>
            <div v-if="apps.length" class="grid gap-1.5">
              <Label for="rule-app">{{ t("billing.accounting.form.app") }}</Label>
              <NativeSelect id="rule-app" v-model="app">
                <option value="">{{ t("billing.accounting.form.wholeTenant") }}</option>
                <option v-for="a in apps" :key="a.id" :value="a.id">{{ a.name }}</option>
              </NativeSelect>
            </div>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="grid gap-1.5">
              <Label for="rule-expected">{{ t("billing.accounting.rules.expected") }}</Label>
              <Input id="rule-expected" v-model="expected" inputmode="decimal" autocomplete="off" />
            </div>
            <div class="grid gap-1.5">
              <Label for="rule-tolerance">{{ t("billing.accounting.rules.tolerance") }}</Label>
              <Input id="rule-tolerance" v-model="tolerance" inputmode="decimal" autocomplete="off" />
            </div>
          </div>
          <label class="flex items-start gap-2 text-sm">
            <Checkbox :model-value="auto" class="mt-0.5" @update:model-value="auto = $event === true" />
            <span>{{ t("billing.accounting.rules.auto") }}<span class="text-muted-foreground block text-xs">{{ t("billing.accounting.rules.autoHint") }}</span></span>
          </label>
          <ErrorText :message="formError" />
          <ErrorText :error="save.error.value" />
          <ErrorText :error="del.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button v-if="editing" type="button" variant="outline" @click="reset">{{ t("billing.accounting.form.cancel") }}</Button>
        <Button type="button" variant="outline" @click="open = false">{{ t("common.close") }}</Button>
        <Button type="submit" form="rule-form" :disabled="save.isPending.value">{{ t("billing.accounting.rules.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
