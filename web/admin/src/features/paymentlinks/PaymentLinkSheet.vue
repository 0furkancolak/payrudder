<script setup lang="ts">
import AppField from "@/app/layout/AppField.vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useTenantCurrency } from "@/features/tenants/currency";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import { newKey } from "@/shared/lib/key";
import { useSeededRef } from "@/shared/lib/seeded";
import { Button } from "@/shared/ui/button";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useFormErrors } from "@/shared/lib/formErrors";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { usePaymentLinkActions } from "./api";
import { absoluteUrl, fieldErrors, toRequest, type LinkForm } from "./lib";

/**
 * Makes a payment link: for an invoice, or for an amount of the tenant's choosing. The page can be reached from an invoice or a customer with the form
 * prefilled (`invoice`, `customer`), and the form follows them when they change. The URL comes back once, right after creation, and is shown here.
 */
const props = defineProps<{ tenantId: string; invoice: string; customer: string }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();
const tenantId = computed(() => props.tenantId);
const act = usePaymentLinkActions(tenantId);

const form = ref<LinkForm>({
  kind: props.invoice ? "invoice" : "adhoc",
  invoiceId: props.invoice,
  amount: "",
  currency: FALLBACK_CURRENCY,
  description: "",
  customer: props.customer,
  promotion: "",
  singleUse: true,
  days: "30",
  key: newKey(),
});
const check = useFormErrors(() => {
  const f = fieldErrors(form.value);
  const msg = (k: "invoice" | "amount" | "description" | "days") => (f[k] ? t(`billing.paymentLinks.errors.${k}`) : undefined);
  return { invoice: msg("invoice"), amount: msg("amount"), description: msg("description"), days: msg("days") };
});
const errs = check.errors;
// An error about the old values must not stay beside the new ones.
watch(form, () => act.create.isError.value && act.create.reset(), { deep: true });
// An ad hoc link is in the tenant's own currency unless the person picks another one.
const seededCurrency = useSeededRef<string>(useTenantCurrency(tenantId), FALLBACK_CURRENCY);
watch(seededCurrency, (c) => (form.value.currency = c), { immediate: true });
watch([() => props.invoice, () => props.customer], ([invoice, customer]) => {
  form.value = { ...form.value, kind: invoice ? "invoice" : form.value.kind, invoiceId: invoice || form.value.invoiceId, customer: customer || form.value.customer };
});
watch(open, (v) => {
  if (!v) return;
  check.reset();
  act.create.reset();
});

const created = computed(() => act.create.data.value);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));

function submit() {
  if (!check.attempt()) return;
  const r = toRequest(form.value);
  if ("error" in r) return;
  act.create.mutate(r.body, { onSuccess: () => (form.value = { ...form.value, key: newKey(), amount: "", description: "", promotion: "" }) });
}
/** After a link is made the sheet shows the link alone; "Make another" brings the empty form back. */
const done = computed(() => !!created.value && act.create.isSuccess.value);
function another() {
  check.reset();
  act.create.reset();
}
</script>

<template>
  <Sheet v-model:open="open" name="new-payment-link">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ done ? t("billing.paymentLinks.createdTitle") : t("billing.paymentLinks.title") }}</SheetTitle>
        <SheetDescription>{{ done ? t("billing.paymentLinks.createdHint") : t("billing.paymentLinks.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form v-if="!done" id="payment-link-form" class="grid gap-4" role="form" novalidate :aria-label="t('billing.paymentLinks.formLabel')" @submit.prevent="submit">
          <AppField />
          <FormField :label="t('billing.paymentLinks.kind')" for="pl-kind">
            <NativeSelect id="pl-kind" v-model="form.kind">
              <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
              <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
            </NativeSelect>
          </FormField>
          <template v-if="form.kind === 'invoice'">
            <FormField :label="t('billing.paymentLinks.invoiceId')" for="pl-invoice" :error="errs.invoice">
              <Input id="pl-invoice" v-model="form.invoiceId" :aria-invalid="errs.invoice ? true : undefined" aria-describedby="pl-invoice-error" />
            </FormField>
          </template>
          <template v-else>
            <FormField :label="t('billing.paymentLinks.amount')" for="pl-amount" :error="errs.amount">
              <Input id="pl-amount" v-model="form.amount" inputmode="decimal" :aria-invalid="errs.amount ? true : undefined" aria-describedby="pl-amount-error" />
            </FormField>
            <FormField :label="t('billing.paymentLinks.currency')" for="pl-currency">
              <CurrencySelect id="pl-currency" v-model="form.currency" :aria-label="t('billing.paymentLinks.currency')" />
            </FormField>
            <FormField :label="t('billing.paymentLinks.description')" for="pl-description" :error="errs.description">
              <Input id="pl-description" v-model="form.description" maxlength="200" :aria-invalid="errs.description ? true : undefined" aria-describedby="pl-description-error" />
            </FormField>
            <FormField :label="t('billing.paymentLinks.customerOptional')" for="pl-customer">
              <CustomerSelect id="pl-customer" v-model="form.customer" :tenant-id="tenantId" :label="t('billing.paymentLinks.customer')" :placeholder="t('billing.paymentLinks.customerNone')" named />
            </FormField>
            <FormField :label="t('billing.paymentLinks.promotionCode')" for="pl-promo" :hint="t('billing.paymentLinks.promotionHint')">
              <Input id="pl-promo" v-model="form.promotion" maxlength="40" />
            </FormField>
          </template>
          <FormField :label="t('billing.paymentLinks.days')" for="pl-days" :error="errs.days">
            <Input id="pl-days" v-model="form.days" inputmode="numeric" :aria-invalid="errs.days ? true : undefined" aria-describedby="pl-days-error" />
          </FormField>
          <label class="flex items-center gap-2 text-sm"><input v-model="form.singleUse" type="checkbox" class="size-4" />{{ t("billing.paymentLinks.singleUse") }}</label>
          <p class="text-muted-foreground text-xs">{{ t(form.kind === "adhoc" ? "billing.paymentLinks.adhocHint" : "billing.paymentLinks.invoiceHint") }}</p>
          <ErrorText :error="act.create.error.value" />
        </form>
        <div v-else class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
          <template v-if="shownUrl">
            <p class="text-sm font-medium">{{ t("billing.paymentLinks.shownOnce") }}</p>
            <CopyField :value="shownUrl" :label="t('billing.paymentLinks.url')" :copy-label="t('billing.paymentLinks.copy')" testid="payment-link-url" />
          </template>
          <p v-else class="text-sm">{{ t("billing.paymentLinks.replayed") }}</p>
        </div>
      </SheetBody>
      <SheetFooter>
        <template v-if="done">
          <Button variant="ghost" type="button" data-testid="payment-link-another" @click="another">{{ t("billing.paymentLinks.another") }}</Button>
          <Button type="button" @click="open = false">{{ t("common.close") }}</Button>
        </template>
        <template v-else>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="payment-link-form" :disabled="act.create.isPending.value">{{ t("billing.paymentLinks.create") }}</Button>
        </template>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
