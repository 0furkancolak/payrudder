<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { taxFormRules } from "@/shared/lib/customerTax";
import { taxIdHint } from "@/shared/lib/taxid";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";

/**
 * The tax part of a customer's form: which fields it shows, what it calls the tax id and what it says about it all follow the customer's country and type
 * (`taxFormRules`). A required tax id that is empty gets `error` from the parent (the parent owns the submit); a number that does not look like the kind the customer
 * should have gets a hint here and is saved all the same.
 */
const props = defineProps<{ idPrefix: string; country?: string; kind?: string; error?: string }>();
const taxId = defineModel<string>("taxId", { default: "" });
const taxOffice = defineModel<string>("taxOffice", { default: "" });
const registered = defineModel<boolean>("registered", { default: false });
const { t } = useI18n();

const rules = computed(() => taxFormRules(props.country, props.kind, { taxId: taxId.value.trim() !== "", taxOffice: taxOffice.value.trim() !== "", eInvoice: registered.value }));
const hint = computed(() => taxIdHint(taxId.value, props.country, props.kind as "individual" | "business" | ""));
const idId = computed(() => `${props.idPrefix}-tax-id`);
</script>

<template>
  <FormField v-if="rules.taxId !== 'hidden'" :label="t(rules.label === 'any' ? 'einvoice.customer.taxId' : `einvoice.customer.labels.${rules.label}`)" :for="idId" :error="error" :hint="rules.about ? t(`einvoice.customer.about.${rules.about}`) : undefined">
    <Input :id="idId" v-model="taxId" maxlength="40" :aria-invalid="error ? true : undefined" :aria-describedby="[error ? `${idId}-error` : '', hint ? `${idId}-hint` : ''].filter(Boolean).join(' ') || undefined" />
    <p v-if="hint" :id="`${idId}-hint`" class="text-warning text-xs" role="status" data-testid="tax-id-hint">{{ t(`einvoice.customer.hint.${hint}`) }}</p>
  </FormField>
  <FormField v-if="rules.taxOffice !== 'hidden'" :label="t('einvoice.customer.taxOffice')" :for="`${idPrefix}-tax-office`">
    <Input :id="`${idPrefix}-tax-office`" v-model="taxOffice" maxlength="120" />
  </FormField>
  <label v-if="rules.eInvoice" class="flex items-center gap-2 text-sm"><input v-model="registered" type="checkbox" :aria-label="t('einvoice.customer.registered')" />{{ t("einvoice.customer.registered") }}</label>
</template>
