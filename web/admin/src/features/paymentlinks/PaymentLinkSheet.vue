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
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { usePaymentLinkActions } from "./api";
import { absoluteUrl, toRequest, type LinkForm } from "./lib";

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
const formError = ref("");
// An ad hoc link is in the tenant's own currency unless the person picks another one.
const seededCurrency = useSeededRef<string>(useTenantCurrency(tenantId), FALLBACK_CURRENCY);
watch(seededCurrency, (c) => (form.value.currency = c), { immediate: true });
watch([() => props.invoice, () => props.customer], ([invoice, customer]) => {
  form.value = { ...form.value, kind: invoice ? "invoice" : form.value.kind, invoiceId: invoice || form.value.invoiceId, customer: customer || form.value.customer };
});
watch(open, (v) => {
  if (!v) return;
  formError.value = "";
  act.create.reset();
});

const created = computed(() => act.create.data.value);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));

function submit() {
  formError.value = "";
  const r = toRequest(form.value);
  if ("error" in r) {
    formError.value = t(`billing.paymentLinks.errors.${r.error}`);
    return;
  }
  act.create.mutate(r.body, { onSuccess: () => (form.value = { ...form.value, key: newKey(), amount: "", description: "", promotion: "" }) });
}
</script>

<template>
  <Sheet v-model:open="open" name="new-payment-link">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.paymentLinks.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.paymentLinks.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="payment-link-form" class="grid gap-4" role="form" :aria-label="t('billing.paymentLinks.formLabel')" @submit.prevent="submit">
          <AppField />
          <NativeSelect v-model="form.kind" :aria-label="t('billing.paymentLinks.kind')">
            <option value="adhoc">{{ t("billing.paymentLinks.kinds.adhoc") }}</option>
            <option value="invoice">{{ t("billing.paymentLinks.kinds.invoice") }}</option>
          </NativeSelect>
          <template v-if="form.kind === 'invoice'">
            <Input v-model="form.invoiceId" :aria-label="t('billing.paymentLinks.invoiceId')" :placeholder="t('billing.paymentLinks.invoiceId')" />
          </template>
          <template v-else>
            <Input v-model="form.amount" inputmode="decimal" :aria-label="t('billing.paymentLinks.amount')" :placeholder="t('billing.paymentLinks.amount')" />
            <CurrencySelect v-model="form.currency" :aria-label="t('billing.paymentLinks.currency')" />
            <Input v-model="form.description" maxlength="200" :aria-label="t('billing.paymentLinks.description')" :placeholder="t('billing.paymentLinks.description')" />
            <Input v-model="form.customer" :aria-label="t('billing.paymentLinks.customer')" :placeholder="t('billing.paymentLinks.customerOptional')" />
            <Input v-model="form.promotion" maxlength="40" :aria-label="t('billing.paymentLinks.promotionCode')" :placeholder="t('billing.paymentLinks.promotionCode')" :title="t('billing.paymentLinks.promotionHint')" />
          </template>
          <Input v-model="form.days" inputmode="numeric" :aria-label="t('billing.paymentLinks.days')" :placeholder="t('billing.paymentLinks.days')" />
          <label class="flex items-center gap-2 text-sm"><input v-model="form.singleUse" type="checkbox" class="size-4" />{{ t("billing.paymentLinks.singleUse") }}</label>
          <p class="text-muted-foreground text-xs">{{ t(form.kind === "adhoc" ? "billing.paymentLinks.adhocHint" : "billing.paymentLinks.invoiceHint") }}</p>
          <ErrorText :message="formError" />
          <ErrorText :error="act.create.error.value" />
        </form>
        <div v-if="created && act.create.isSuccess.value" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
          <template v-if="shownUrl">
            <p class="text-sm font-medium">{{ t("billing.paymentLinks.shownOnce") }}</p>
            <CopyField :value="shownUrl" :label="t('billing.paymentLinks.url')" :copy-label="t('billing.paymentLinks.copy')" testid="payment-link-url" />
          </template>
          <p v-else class="text-sm">{{ t("billing.paymentLinks.replayed") }}</p>
        </div>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="payment-link-form" :disabled="act.create.isPending.value">{{ t("billing.paymentLinks.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
