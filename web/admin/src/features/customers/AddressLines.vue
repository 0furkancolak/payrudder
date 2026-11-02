<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { computed } from "vue";
import { ADDRESS_MAX, countryProfile, postalCodeLooksOff } from "@/shared/lib/address";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";

/**
 * The street lines and the postal code of a customer's address, the parts that are not places (country, region and city are the LocationFields). Each has a visible
 * label. Given the country, the postal code is called what that country calls it (ZIP code, postcode), shows an example, and says when it does not look like one there
 * (accepted all the same).
 */
const props = withDefaults(defineProps<{ idPrefix?: string; country?: string }>(), { idPrefix: "address", country: "" });
const line1 = defineModel<string | undefined>("line1");
const line2 = defineModel<string | undefined>("line2");
const postalCode = defineModel<string | undefined>("postalCode");
const { t } = useI18n();
const profile = computed(() => countryProfile(props.country));
const postalLabel = computed(() => (props.country ? t(`address.labels.${profile.value.postalCode}`) : t("billing.customers.address.postalCode")));
const postalOff = computed(() => postalCodeLooksOff(props.country, postalCode.value));
</script>

<template>
  <FormField :label="t('billing.customers.address.line1')" :for="`${idPrefix}-line1`"><Input :id="`${idPrefix}-line1`" v-model="line1" :maxlength="ADDRESS_MAX.line1" /></FormField>
  <FormField :label="t('billing.customers.address.line2')" :for="`${idPrefix}-line2`"><Input :id="`${idPrefix}-line2`" v-model="line2" :maxlength="ADDRESS_MAX.line2" /></FormField>
  <FormField :label="postalLabel" :for="`${idPrefix}-postal`">
    <Input :id="`${idPrefix}-postal`" v-model="postalCode" :maxlength="ADDRESS_MAX.postal_code" :placeholder="country ? profile.example : undefined" :aria-describedby="postalOff ? `${idPrefix}-postal-hint` : undefined" />
    <p v-if="postalOff" :id="`${idPrefix}-postal-hint`" class="text-warning text-xs" role="status">{{ t("address.postalHint", { example: profile.example }) }}</p>
  </FormField>
</template>
