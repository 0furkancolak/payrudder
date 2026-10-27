<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppField from "@/app/layout/AppField.vue";
import { useI18n } from "vue-i18n";
import { cleanAddress } from "@/shared/lib/address";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { useFormErrors } from "@/shared/lib/formErrors";
import { toApiError } from "@/shared/api/client";
import { useErrorText } from "@/shared/lib/errors";
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
const errorText = useErrorText();

const externalId = ref("");
const name = ref("");
const email = ref("");
const country = ref("");
const currency = ref("");
const address = ref<Address>({});
/** The place fields carry the names the customer list uses for them. */
const placeLabels = computed(() => ({ country: t("billing.customers.country"), region: t("billing.customers.address.region"), city: t("billing.customers.address.city") }));

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const form = useFormErrors(() => ({
  externalId: externalId.value.trim() === "" ? t("common.validation.required") : undefined,
  name: name.value.trim() === "" ? t("common.validation.required") : undefined,
  email: email.value.trim() === "" ? t("common.validation.required") : !EMAIL_RE.test(email.value.trim()) ? t("common.validation.email") : undefined,
}));
/** A customer id that is taken is the id field's problem, so it is shown there and not above the buttons. */
const duplicate = computed(() => {
  const e = create.error.value;
  if (!e) return "";
  const a = toApiError(e);
  return a.code === "ALREADY_EXISTS" || a.key.endsWith("already_exists") ? errorText(create.error.value) : "";
});
const idError = computed(() => form.errors.value.externalId ?? duplicate.value);
// An error about the old values must not stay beside the new ones.
watch([externalId, name, email, country, currency, address], () => create.reset(), { deep: true });

function reset() {
  externalId.value = name.value = email.value = country.value = currency.value = "";
  address.value = {};
}
// A sheet that was closed opens clean: the last values and error are not shown again.
watch(open, (v) => {
  if (v) {
    reset();
    create.reset();
    form.reset();
  }
});

function submit() {
  if (!form.attempt()) return;
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
        <form id="create-customer" class="grid gap-3" role="form" novalidate :aria-label="t('billing.customers.formLabel')" @submit.prevent="submit">
          <AppField />
          <FormField :label="t('billing.customers.externalId')" for="new-customer-id" :error="idError">
            <Input id="new-customer-id" v-model="externalId" maxlength="128" :aria-invalid="idError ? true : undefined" aria-describedby="new-customer-id-error" />
          </FormField>
          <FormField :label="t('billing.customers.name')" for="new-customer-name" :error="form.errors.value.name">
            <Input id="new-customer-name" v-model="name" :aria-invalid="form.errors.value.name ? true : undefined" aria-describedby="new-customer-name-error" />
          </FormField>
          <FormField :label="t('billing.customers.email')" for="new-customer-email" :error="form.errors.value.email">
            <Input id="new-customer-email" v-model="email" type="email" :aria-invalid="form.errors.value.email ? true : undefined" aria-describedby="new-customer-email-error" />
          </FormField>
          <FormField :label="t('billing.customers.currency')" for="new-customer-currency">
            <CurrencySelect id="new-customer-currency" v-model="currency" :aria-label="t('billing.customers.currency')" :blank="t('billing.customers.tenantCurrency')" />
          </FormField>
          <fieldset class="grid gap-3">
            <legend class="text-muted-foreground mb-1 text-sm">{{ t("billing.customers.address.title") }}</legend>
            <LocationFields v-model:country="country" v-model:region="address.region" v-model:city="address.city" id-prefix="new-customer" :labels="placeLabels" />
            <AddressLines v-model:line1="address.line1" v-model:line2="address.line2" v-model:postal-code="address.postal_code" id-prefix="new-customer" />
          </fieldset>
          <ErrorText v-if="!duplicate" :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-customer" :disabled="create.isPending.value">{{ t("billing.customers.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
