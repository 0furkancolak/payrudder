<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { minorToMajor, parseMajor } from "@/shared/lib/money";
import { useFormErrors } from "@/shared/lib/formErrors";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { CATEGORIES, useSaveExpense, type Category, type Expense } from "./api";
import { ymd } from "@/shared/lib/dates";
import { CurrencySelect } from "@/shared/ui/search-select";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

/** Adds an expense, or edits `expense`. A recurring expense is edited as a whole: every occurrence changes with it. */
const props = defineProps<{ tenantId: string; expense: Expense | null; defaultCurrency: string; apps: { id: string; name: string }[]; defaultApp?: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const save = useSaveExpense(() => props.tenantId);

const vendor = ref("");
const category = ref<Category>("infrastructure");
const amount = ref("");
const tax = ref("");
const currency = ref<string>(FALLBACK_CURRENCY);
const incurredOn = ref("");
const recurring = ref<"none" | "monthly" | "yearly">("none");
const until = ref("");
const app = ref("");
const note = ref("");
const check = useFormErrors(() => {
  const minor = parseMajor(amount.value, currency.value);
  const taxMinor = tax.value.trim() === "" ? undefined : parseMajor(tax.value, currency.value);
  return {
    vendor: vendor.value.trim() === "" ? t("common.validation.required") : undefined,
    amount: minor === null || minor === "0" ? t("billing.accounting.form.bad", { currency: currency.value }) : undefined,
    tax: taxMinor === null || (taxMinor !== undefined && minor !== null && BigInt(taxMinor) > BigInt(minor)) ? t("billing.accounting.form.bad", { currency: currency.value }) : undefined,
    date: incurredOn.value === "" || (recurring.value !== "none" && until.value !== "" && until.value < incurredOn.value) ? t("billing.accounting.form.badDates") : undefined,
  };
});
const errs = check.errors;
// An error about the old values must not stay beside the new ones.
watch([vendor, amount, tax, currency, incurredOn, until, recurring], () => save.isError.value && save.reset());

// Every time the dialog opens it starts from the expense being edited, or from a blank one dated today.
watch(open, (isOpen) => {
  if (!isOpen) return;
  const e = props.expense;
  vendor.value = e?.vendor ?? "";
  category.value = e?.category ?? "infrastructure";
  currency.value = e?.currency ?? props.defaultCurrency;
  amount.value = e ? minorToMajor(e.amount_minor, e.currency ?? currency.value) : "";
  tax.value = e?.tax_minor && e.tax_minor !== "0" ? minorToMajor(e.tax_minor, e.currency ?? currency.value) : "";
  incurredOn.value = e?.incurred_on ?? ymd(new Date());
  recurring.value = (e?.recurring as "none" | "monthly" | "yearly" | undefined) ?? "none";
  until.value = e?.recurring_until ?? "";
  // A new expense starts on the app the page is about; "General accounting" (no app) is for what belongs to the organisation as a whole.
  app.value = e ? (e.app_id ?? "") : (props.defaultApp ?? "");
  note.value = e?.note ?? "";
  check.reset();
  save.reset();
});

function submit() {
  if (!check.attempt()) return;
  const minor = parseMajor(amount.value, currency.value) ?? "0";
  const taxMinor = tax.value.trim() === "" ? undefined : (parseMajor(tax.value, currency.value) ?? undefined);
  save.mutate(
    {
      id: props.expense?.id,
      body: {
        vendor: vendor.value.trim(),
        category: category.value,
        amount_minor: minor,
        tax_minor: taxMinor,
        attachment_asset_id: props.expense?.attachment_asset_id || undefined,
        currency: currency.value,
        incurred_on: incurredOn.value,
        recurring: recurring.value,
        recurring_until: recurring.value !== "none" && until.value !== "" ? until.value : undefined,
        app_id: app.value || undefined,
        note: note.value.trim() || undefined,
      },
    },
    { onSuccess: () => (open.value = false) },
  );
}
</script>

<template>
  <Sheet v-model:open="open" :name="expense ? undefined : 'new-expense'">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ expense ? t("billing.accounting.form.editTitle") : t("billing.accounting.form.addTitle") }}</SheetTitle>
        <SheetDescription>{{ t("billing.accounting.form.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
      <form id="expense-form" class="grid gap-3" novalidate @submit.prevent="submit">
        <div v-if="apps.length" data-testid="expense-app" class="grid gap-1.5">
          <Label for="exp-app">{{ t("billing.accounting.form.app") }}</Label>
          <NativeSelect id="exp-app" v-model="app">
            <option value="">{{ t("billing.accounting.form.wholeTenant") }}</option>
            <option v-for="a in apps" :key="a.id" :value="a.id">{{ a.name }}</option>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="exp-vendor">{{ t("billing.accounting.form.vendor") }}</Label>
          <Input id="exp-vendor" v-model="vendor" maxlength="120" autocomplete="off" :aria-invalid="errs.vendor ? true : undefined" aria-describedby="exp-vendor-error" />
          <p v-if="errs.vendor" id="exp-vendor-error" class="text-destructive text-xs" role="alert">{{ errs.vendor }}</p>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1.5">
            <Label for="exp-category">{{ t("billing.accounting.form.category") }}</Label>
            <NativeSelect id="exp-category" v-model="category">
              <option v-for="c in CATEGORIES" :key="c" :value="c">{{ t(`billing.accounting.category.${c}`) }}</option>
            </NativeSelect>
          </div>
          <div class="grid gap-1.5">
            <Label for="exp-date">{{ t("billing.accounting.form.date") }}</Label>
            <Input id="exp-date" v-model="incurredOn" type="date" :aria-invalid="errs.date ? true : undefined" aria-describedby="exp-date-error" />
            <p v-if="errs.date" id="exp-date-error" class="text-destructive text-xs" role="alert">{{ errs.date }}</p>
          </div>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1.5">
            <Label for="exp-amount">{{ t("billing.accounting.form.amount") }}</Label>
            <Input id="exp-amount" v-model="amount" inputmode="decimal" autocomplete="off" :aria-invalid="errs.amount ? true : undefined" aria-describedby="exp-amount-error" />
            <p v-if="errs.amount" id="exp-amount-error" class="text-destructive text-xs" role="alert">{{ errs.amount }}</p>
          </div>
          <div class="grid gap-1.5">
            <Label for="exp-currency">{{ t("billing.accounting.form.currency") }}</Label>
            <CurrencySelect id="exp-currency" v-model="currency" />
          </div>
        </div>
        <div class="grid gap-1.5">
          <Label for="exp-tax">{{ t("billing.accounting.scan.tax") }}</Label>
          <Input id="exp-tax" v-model="tax" inputmode="decimal" autocomplete="off" :aria-invalid="errs.tax ? true : undefined" aria-describedby="exp-tax-error" />
          <p v-if="errs.tax" id="exp-tax-error" class="text-destructive text-xs" role="alert">{{ errs.tax }}</p>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1.5">
            <Label for="exp-recurring">{{ t("billing.accounting.form.recurring") }}</Label>
            <NativeSelect id="exp-recurring" v-model="recurring">
              <option value="none">{{ t("billing.accounting.recurring.none") }}</option>
              <option value="monthly">{{ t("billing.accounting.recurring.monthly") }}</option>
              <option value="yearly">{{ t("billing.accounting.recurring.yearly") }}</option>
            </NativeSelect>
          </div>
          <div v-if="recurring !== 'none'" class="grid gap-1.5">
            <Label for="exp-until">{{ t("billing.accounting.form.until") }}</Label>
            <Input id="exp-until" v-model="until" type="date" />
          </div>
        </div>
        <p v-if="recurring !== 'none'" class="text-muted-foreground text-xs">{{ t("billing.accounting.form.recurringHint") }}</p>
        <div class="grid gap-1.5">
          <Label for="exp-note">{{ t("billing.accounting.form.note") }}</Label>
          <Textarea id="exp-note" v-model="note" rows="2" maxlength="500" />
        </div>
        <ErrorText :error="save.error.value" />
      </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="outline" @click="open = false">{{ t("billing.accounting.form.cancel") }}</Button>
        <Button type="submit" form="expense-form" :disabled="save.isPending.value">{{ t("billing.accounting.form.save") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
