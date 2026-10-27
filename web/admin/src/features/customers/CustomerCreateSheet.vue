<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppField from "@/app/layout/AppField.vue";
import { useI18n } from "vue-i18n";
import { cleanAddress } from "@/shared/lib/address";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { LocationFields } from "@/shared/ui/location-fields";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreateCustomer, type Address } from "./api";
import AddressLines from "./AddressLines.vue";

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
/** The place fields carry the names the customer list uses for them. */
const placeLabels = computed(() => ({ country: t("billing.customers.country"), region: t("billing.customers.address.region"), city: t("billing.customers.address.city") }));

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
  const a = cleanAddress(address.value);
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
  <Sheet v-model:open="open" name="new-customer">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.customers.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.customers.newHint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-customer" class="grid gap-3" role="form" :aria-label="t('billing.customers.formLabel')" @submit.prevent="submit">
          <AppField />
          <Input v-model="externalId" :aria-label="t('billing.customers.externalId')" :placeholder="t('billing.customers.externalId')" maxlength="128" required />
          <Input v-model="name" :aria-label="t('billing.customers.name')" :placeholder="t('billing.customers.name')" required />
          <Input v-model="email" type="email" :aria-label="t('billing.customers.email')" :placeholder="t('billing.customers.email')" required />
          <CurrencySelect v-model="currency" :aria-label="t('billing.customers.currency')" :blank="t('billing.customers.tenantCurrency')" />
          <fieldset class="grid gap-3">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.customers.address.title") }}</legend>
            <LocationFields v-model:country="country" v-model:region="address.region" v-model:city="address.city" id-prefix="new-customer" compact :labels="placeLabels" />
            <AddressLines v-model:line1="address.line1" v-model:line2="address.line2" v-model:postal-code="address.postal_code" />
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
