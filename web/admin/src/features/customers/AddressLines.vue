<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { ADDRESS_MAX } from "@/shared/lib/address";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";

/** The street lines and the postal code of a customer's address, the parts that are not places (country, region and city are the LocationFields). Each has a visible label. */
withDefaults(defineProps<{ idPrefix?: string }>(), { idPrefix: "address" });
const line1 = defineModel<string | undefined>("line1");
const line2 = defineModel<string | undefined>("line2");
const postalCode = defineModel<string | undefined>("postalCode");
const { t } = useI18n();
</script>

<template>
  <FormField :label="t('billing.customers.address.line1')" :for="`${idPrefix}-line1`"><Input :id="`${idPrefix}-line1`" v-model="line1" :maxlength="ADDRESS_MAX.line1" /></FormField>
  <FormField :label="t('billing.customers.address.line2')" :for="`${idPrefix}-line2`"><Input :id="`${idPrefix}-line2`" v-model="line2" :maxlength="ADDRESS_MAX.line2" /></FormField>
  <FormField :label="t('billing.customers.address.postalCode')" :for="`${idPrefix}-postal`"><Input :id="`${idPrefix}-postal`" v-model="postalCode" :maxlength="ADDRESS_MAX.postal_code" /></FormField>
</template>
