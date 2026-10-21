<script setup lang="ts">
import { reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { CURRENCIES } from "@/shared/lib/currencies";
import { Checkbox } from "@/shared/ui/checkbox";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { buildBankSettings, emptyBankForm, type BankErrors, type BankField, type BankSettings } from "./banktransfer";

/**
 * The structured form for the "banktransfer" provider account. It owns its fields; the page asks for the settings with build(), which checks them
 * (the IBAN by its check digits) and returns null, showing what is wrong, when they cannot be sent.
 */
defineProps<{ environment: "sandbox" | "live" }>();
const { t } = useI18n();
const form = reactive(emptyBankForm());
const errors = ref<BankErrors>({});

function build(): BankSettings | null {
  const r = buildBankSettings(form);
  errors.value = r.ok ? {} : r.errors;
  return r.ok ? r.settings : null;
}
function reset() {
  Object.assign(form, emptyBankForm());
  errors.value = {};
}
defineExpose({ build, reset });

function toggle(currency: string, on: boolean | "indeterminate") {
  const without = form.currencies.filter((c) => c !== currency);
  form.currencies = on === true ? [...without, currency] : without;
}
const message = (field: BankField) => (errors.value[field] ? t(`billing.providers.bank.errors.${errors.value[field]}`) : "");
</script>

<template>
  <fieldset class="grid gap-3" :aria-label="t('billing.providers.bank.title')">
    <p v-if="environment === 'sandbox'" class="text-muted-foreground bg-muted rounded-lg p-3 text-sm" role="note">{{ t("billing.providers.bank.sandboxNote") }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <div class="grid gap-1.5">
        <Label for="bank-name">{{ t("billing.providers.bank.bankName") }}</Label>
        <Input id="bank-name" v-model="form.bankName" maxlength="120" autocomplete="off" :aria-invalid="!!errors.bankName" />
        <p v-if="errors.bankName" class="text-destructive text-xs" role="alert">{{ message("bankName") }}</p>
      </div>
      <div class="grid gap-1.5">
        <Label for="bank-holder">{{ t("billing.providers.bank.accountHolder") }}</Label>
        <Input id="bank-holder" v-model="form.accountHolder" maxlength="120" autocomplete="off" :aria-invalid="!!errors.accountHolder" />
        <p v-if="errors.accountHolder" class="text-destructive text-xs" role="alert">{{ message("accountHolder") }}</p>
      </div>
      <div class="grid gap-1.5">
        <Label for="bank-iban">{{ t("billing.providers.bank.iban") }}</Label>
        <Input id="bank-iban" v-model="form.iban" class="mono" spellcheck="false" autocomplete="off" :aria-invalid="!!errors.iban" />
        <p v-if="errors.iban" class="text-destructive text-xs" role="alert">{{ message("iban") }}</p>
        <p v-else class="text-muted-foreground text-xs">{{ t("billing.providers.bank.ibanHint") }}</p>
      </div>
      <div class="grid gap-1.5">
        <Label for="bank-bic">{{ t("billing.providers.bank.bic") }}</Label>
        <Input id="bank-bic" v-model="form.bic" class="mono" maxlength="11" spellcheck="false" autocomplete="off" :aria-invalid="!!errors.bic" />
        <p v-if="errors.bic" class="text-destructive text-xs" role="alert">{{ message("bic") }}</p>
      </div>
      <div class="grid gap-1.5">
        <Label for="bank-reference">{{ t("billing.providers.bank.referenceTemplate") }}</Label>
        <Input id="bank-reference" v-model="form.referenceTemplate" class="mono" maxlength="60" spellcheck="false" autocomplete="off" :aria-invalid="!!errors.referenceTemplate" :placeholder="'{invoice_number}'" />
        <p v-if="errors.referenceTemplate" class="text-destructive text-xs" role="alert">{{ message("referenceTemplate") }}</p>
        <p v-else class="text-muted-foreground text-xs">{{ t("billing.providers.bank.referenceHint", { number: "{invoice_number}", id: "{invoice_id}" }) }}</p>
      </div>
      <div class="grid gap-1.5">
        <Label for="bank-window">{{ t("billing.providers.bank.windowDays") }}</Label>
        <Input id="bank-window" v-model="form.windowDays" inputmode="numeric" class="w-32" autocomplete="off" :aria-invalid="!!errors.windowDays" />
        <p v-if="errors.windowDays" class="text-destructive text-xs" role="alert">{{ message("windowDays") }}</p>
        <p v-else class="text-muted-foreground text-xs">{{ t("billing.providers.bank.windowHint") }}</p>
      </div>
    </div>
    <div class="grid gap-1.5" role="group" :aria-label="t('billing.providers.bank.currencies')">
      <span class="text-sm font-medium">{{ t("billing.providers.bank.currencies") }}</span>
      <div class="flex flex-wrap gap-4">
        <label v-for="c in CURRENCIES" :key="c" class="flex items-center gap-2 text-sm">
          <Checkbox :model-value="form.currencies.includes(c)" :aria-label="c" @update:model-value="toggle(c, $event)" />{{ c }}
        </label>
      </div>
      <p class="text-muted-foreground text-xs">{{ t("billing.providers.bank.currenciesHint") }}</p>
    </div>
  </fieldset>
</template>
