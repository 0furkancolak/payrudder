<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { computed } from "vue";
import { API_LOCALES } from "@/shared/lib/locale";
import { normalizePhone } from "@/shared/lib/phone";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";

/**
 * What decides how a customer is addressed and billed: the language of its mails, receipts and payment pages, whether it is a person or a business (a taxpayer), and
 * the mobile number providers and notices may need. All can stay empty. The languages are written in themselves, so a customer's language is found by who speaks it.
 * The number is checked as the server checks it; the parent form blocks the save while `normalizePhone` of it is null.
 */
defineProps<{ idPrefix: string }>();
const locale = defineModel<string>("locale", { default: "" });
const kind = defineModel<string>("kind", { default: "" });
const phone = defineModel<string>("phone", { default: "" });
const { t } = useI18n();
const phoneError = computed(() => (normalizePhone(phone.value) === null ? t("billing.customers.profile.phoneInvalid") : undefined));
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
  <FormField :label="t('billing.customers.profile.phone')" :for="`${idPrefix}-phone`" :error="phoneError" :hint="t('billing.customers.profile.phoneHint')">
    <Input :id="`${idPrefix}-phone`" v-model="phone" type="tel" autocomplete="off" dir="ltr" maxlength="40" :aria-invalid="phoneError ? true : undefined" :aria-describedby="`${idPrefix}-phone-error`" />
  </FormField>
</template>
