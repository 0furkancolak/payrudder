<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useMeters } from "@/features/meters/api";
import { useFormErrors } from "@/shared/lib/formErrors";
import { useAttemptKey } from "@/shared/lib/key";
import { sheetName } from "@/shared/lib/sheets";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { CorrectionRefused, useCorrectUsage } from "./api";

/**
 * Corrects one usage event of a customer: the event is named by the idempotency key the app sent it with, the quantity is what it should have been (0 cancels it)
 * and a reason is kept with the correction. The event is not edited: the difference is added to the period it belongs to, as late usage on a later invoice once the period
 * is closed. Only meters that add (a sum or a count, without a property filter) can be corrected, so only those are offered.
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();

const meters = useMeters(() => props.tenantId);
const correctable = computed(() =>
  (meters.data.value ?? []).filter((m) => (m.aggregation === "AGGREGATION_SUM" || m.aggregation === "AGGREGATION_COUNT") && Object.keys(m.filter ?? {}).length === 0),
);

const correct = useCorrectUsage(() => props.tenantId);
// One key per correction: a double click or a retry after a timeout corrects once; a new correction (after success) gets a new key.
const attempt = useAttemptKey();
const meter = ref("");
const corrects = ref("");
const quantity = ref("");
const reason = ref("");
const check = useFormErrors(() => ({
  meter: meter.value ? undefined : t("billing.corrections.errors.meter"),
  corrects: corrects.value.trim() !== "" && /^[A-Za-z0-9_.:-]{1,128}$/.test(corrects.value.trim()) ? undefined : t("billing.corrections.errors.corrects"),
  quantity: /^\d+(\.\d+)?$/.test(quantity.value.trim()) ? undefined : t("billing.corrections.errors.quantity"),
  reason: reason.value.trim() !== "" ? undefined : t("billing.corrections.errors.reason"),
}));
const errs = check.errors;
watch([meter, corrects, quantity, reason], () => correct.isError.value && correct.reset());

// A sheet that was closed opens clean.
watch(open, (v) => {
  if (!v) return;
  meter.value = corrects.value = quantity.value = reason.value = "";
  check.reset();
  correct.reset();
});

// A refusal says why in the reader's language; any other failure is the API's own error.
const refusal = computed(() => {
  const e = correct.error.value;
  if (!(e instanceof CorrectionRefused)) return undefined;
  const key = `billing.corrections.codes.${e.code}`;
  return t(key) === key ? e.message : t(key);
});

function submit() {
  if (props.customer === "" || !check.attempt()) return;
  correct.mutate(
    { customer: props.customer, meter: meter.value, corrects: corrects.value.trim(), quantity: quantity.value.trim(), reason: reason.value.trim(), key: `corr_${attempt.key.value}` },
    {
      onSuccess: () => {
        attempt.renew();
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open" :name="customer ? sheetName('correct-usage', customer) : undefined">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.corrections.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.corrections.hint", { customer }) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="correct-form" class="grid gap-4" role="form" novalidate :aria-label="t('billing.corrections.formLabel')" @submit.prevent="submit">
          <FormField :label="t('billing.corrections.meter')" for="correct-meter" :error="errs.meter">
            <NativeSelect id="correct-meter" v-model="meter" data-testid="correct-meter" :aria-invalid="errs.meter ? true : undefined" aria-describedby="correct-meter-error">
              <option value="" disabled>{{ t("billing.corrections.pickMeter") }}</option>
              <option v-for="m in correctable" :key="m.code" :value="m.code">{{ m.code }}</option>
            </NativeSelect>
          </FormField>
          <FormField :label="t('billing.corrections.corrects')" for="correct-key" :hint="t('billing.corrections.correctsHint')" :error="errs.corrects">
            <Input id="correct-key" v-model="corrects" class="mono" dir="ltr" autocomplete="off" data-testid="correct-key" :aria-invalid="errs.corrects ? true : undefined" aria-describedby="correct-key-error" />
          </FormField>
          <FormField :label="t('billing.corrections.quantity')" for="correct-quantity" :hint="t('billing.corrections.quantityHint')" :error="errs.quantity">
            <Input id="correct-quantity" v-model="quantity" inputmode="decimal" dir="ltr" data-testid="correct-quantity" :aria-invalid="errs.quantity ? true : undefined" aria-describedby="correct-quantity-error" />
          </FormField>
          <FormField :label="t('billing.corrections.reason')" for="correct-reason" :error="errs.reason">
            <Input id="correct-reason" v-model="reason" maxlength="500" data-testid="correct-reason" :aria-invalid="errs.reason ? true : undefined" aria-describedby="correct-reason-error" />
          </FormField>
          <ErrorText v-if="refusal" :message="refusal" data-testid="correct-refusal" />
          <ErrorText v-else :error="correct.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="correct-form" :disabled="correct.isPending.value" data-testid="correct-submit">{{ t("billing.corrections.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
