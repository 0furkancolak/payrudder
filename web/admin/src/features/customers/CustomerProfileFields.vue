<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { API_LOCALES } from "@/shared/lib/locale";
import { FormField } from "@/shared/ui/form-field";
import { NativeSelect } from "@/shared/ui/native-select";

/**
 * The two things about a customer that decide how it is addressed and billed: the language of its mails, receipts and payment pages, and whether it is a
 * person or a business (a taxpayer). Both can stay empty. The languages are written in themselves, so a customer's language is found by who speaks it.
 */
defineProps<{ idPrefix: string }>();
const locale = defineModel<string>("locale", { default: "" });
const kind = defineModel<string>("kind", { default: "" });
const { t } = useI18n();
const NAMES: Record<(typeof API_LOCALES)[number], string> = { en: "English", tr: "Türkçe", de: "Deutsch", fr: "Français", es: "Español", ar: "العربية" };
</script>

<template>
  <FormField :label="t('billing.customers.profile.language')" :for="`${idPrefix}-locale`">
    <NativeSelect :id="`${idPrefix}-locale`" v-model="locale" class="w-full">
      <option value="">{{ t("billing.customers.profile.auto") }}</option>
      <option v-for="l in API_LOCALES" :key="l" :value="l">{{ NAMES[l] }}</option>
    </NativeSelect>
  </FormField>
  <FormField :label="t('billing.customers.profile.type')" :for="`${idPrefix}-type`">
    <NativeSelect :id="`${idPrefix}-type`" v-model="kind" class="w-full">
      <option value="">{{ t("billing.customers.profile.unknown") }}</option>
      <option value="individual">{{ t("billing.customers.profile.individual") }}</option>
      <option value="business">{{ t("billing.customers.profile.business") }}</option>
    </NativeSelect>
  </FormField>
</template>
