<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreateCustomer, type Address } from "./api";

/** Adds a customer by hand: id, name, e-mail, and optionally a country, a billing currency and an address. */
const props = defineProps<{ tenantId: string }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();
const create = useCreateCustomer(() => props.tenantId);

const externalId = ref("");
const name = ref("");
const email = ref("");
const country = ref("");
const currency = ref("");
const address = ref<Address>({});
const trimmed = (a: Address): Address => Object.fromEntries(Object.entries(a).map(([k, v]) => [k, (v ?? "").trim()]).filter(([, v]) => v !== ""));

function reset() {
  externalId.value = name.value = email.value = country.value = currency.value = "";
  address.value = {};
}
// A sheet that was closed opens clean: the last values and error are not shown again.
watch(open, (v) => {
  if (v) {
    reset();
    create.reset();
  }
});

function submit() {
  const cc = country.value.trim().toUpperCase();
  const a = trimmed(address.value);
  create.mutate(
    {
      external_id: externalId.value.trim(),
      name: name.value.trim(),
      email: email.value.trim(),
      country: cc || undefined,
      billing_currency: currency.value || undefined,
      address: Object.keys(a).length ? { ...a, ...(cc ? { country: cc } : {}) } : undefined,
    },
    {
      onSuccess: () => {
        reset();
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.newHint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-customer" class="grid gap-3" role="form" :aria-label="t('billing.customers.formLabel')" @submit.prevent="submit">
          <Input v-model="externalId" :aria-label="t('billing.customers.externalId')" :placeholder="t('billing.customers.externalId')" maxlength="128" required />
          <Input v-model="name" :aria-label="t('billing.customers.name')" :placeholder="t('billing.customers.name')" required />
          <Input v-model="email" type="email" :aria-label="t('billing.customers.email')" :placeholder="t('billing.customers.email')" required />
          <div class="grid gap-3 sm:grid-cols-2">
            <Input v-model="country" :aria-label="t('billing.customers.country')" :placeholder="t('billing.customers.countryPlaceholder')" maxlength="2" pattern="[A-Za-z]{2}" />
            <CurrencySelect v-model="currency" :aria-label="t('billing.customers.currency')" :blank="t('billing.customers.tenantCurrency')" />
          </div>
          <fieldset class="grid gap-3">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.customers.address.title") }}</legend>
            <Input v-model="address.line1" maxlength="120" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
            <Input v-model="address.line2" maxlength="120" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
            <div class="grid gap-3 sm:grid-cols-2">
              <Input v-model="address.city" maxlength="120" :aria-label="t('billing.customers.address.city')" :placeholder="t('billing.customers.address.city')" />
              <Input v-model="address.postal_code" maxlength="20" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
            </div>
            <Input v-model="address.region" maxlength="120" :aria-label="t('billing.customers.address.region')" :placeholder="t('billing.customers.address.region')" />
          </fieldset>
          <ErrorText :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-customer" :disabled="create.isPending.value">{{ t("billing.customers.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
