<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { ADDRESS_MAX } from "@/shared/lib/address";
import { Input } from "@/shared/ui/input";

/** The street lines and the postal code of a customer's address, the parts that are not places (country, region and city are the LocationFields). */
const line1 = defineModel<string | undefined>("line1");
const line2 = defineModel<string | undefined>("line2");
const postalCode = defineModel<string | undefined>("postalCode");
const { t } = useI18n();
</script>

<template>
  <Input v-model="line1" :maxlength="ADDRESS_MAX.line1" :aria-label="t('billing.customers.address.line1')" :placeholder="t('billing.customers.address.line1')" />
  <Input v-model="line2" :maxlength="ADDRESS_MAX.line2" :aria-label="t('billing.customers.address.line2')" :placeholder="t('billing.customers.address.line2')" />
  <Input v-model="postalCode" :maxlength="ADDRESS_MAX.postal_code" :aria-label="t('billing.customers.address.postalCode')" :placeholder="t('billing.customers.address.postalCode')" />
</template>
