<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAttemptKey } from "@/shared/lib/key";
import { sheetName } from "@/shared/lib/sheets";
import { CREDITS_RE } from "@/shared/lib/rules";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { useFormErrors } from "@/shared/lib/formErrors";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useGrantCredits } from "./api";
import { EXPIRY_CHOICES, expiryTime, REASONS, reasonText, sourceOf, type ExpiryChoice, type Reason } from "./grant";

/**
 * Grants free credits to one customer: how many, why (a welcome gift, goodwill, a promotion) with an optional note, and when they run out. The credits
 * are in the customer's balance at once and are not invoiced. Used from the credits page and from a customer's sheet, so both grant the same way.
 */
const props = defineProps<{ tenantId: string; customer: string }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();

const grant = useGrantCredits(() => props.tenantId);
// One key per grant attempt: a double click or a retry after a timeout adds the credits once; a new grant (after success) gets a new key.
const attempt = useAttemptKey();
const credits = ref("");
const reason = ref<Reason>("welcome");
const note = ref("");
const expiry = ref<ExpiryChoice>("never");
const date = ref("");
const check = useFormErrors(() => ({
  credits: CREDITS_RE.test(credits.value.trim()) ? undefined : t("billing.credits.grant.errors.credits"),
  date: expiry.value === "date" && expiryTime(expiry.value, date.value) === undefined ? t("billing.credits.grant.badExpiry") : undefined,
}));
const errs = check.errors;
watch([credits, reason, note, expiry, date], () => grant.isError.value && grant.reset());

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(open, (v) => {
  if (!v) return;
  credits.value = note.value = date.value = "";
  check.reset();
  reason.value = "welcome";
  expiry.value = "never";
  grant.reset();
});

function submit() {
  if (props.customer === "" || !check.attempt()) return;
  const expireTime = expiryTime(expiry.value, date.value);
  if (expireTime === undefined) return;
  grant.mutate(
    { customer: props.customer, credits: credits.value.trim(), source: sourceOf(reason.value), expireTime: expireTime ?? undefined, reason: reasonText(reason.value, note.value), key: attempt.key.value },
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
  <Sheet v-model:open="open" :name="customer ? sheetName('grant-credits', customer) : undefined">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.credits.grant.free") }}</SheetTitle>
        <SheetDescription>{{ t("billing.credits.grant.freeHint", { customer }) }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="grant-form" class="grid gap-4" role="form" novalidate :aria-label="t('billing.credits.grant.formLabel')" @submit.prevent="submit">
          <FormField :label="t('billing.credits.grant.credits')" for="grant-credits" :error="errs.credits">
            <Input id="grant-credits" v-model="credits" inputmode="numeric" data-testid="grant-credits" :aria-invalid="errs.credits ? true : undefined" aria-describedby="grant-credits-error" />
          </FormField>
          <FormField :label="t('billing.credits.grant.source')" for="grant-reason">
            <NativeSelect id="grant-reason" v-model="reason" data-testid="grant-reason">
              <option v-for="r in REASONS" :key="r" :value="r">{{ t(`billing.credits.grant.reasons.${r}`) }}</option>
            </NativeSelect>
          </FormField>
          <FormField :label="t('billing.credits.grant.reason')" for="grant-note">
            <Input id="grant-note" v-model="note" maxlength="150" />
          </FormField>
          <FormField :label="t('billing.credits.grant.expires')" for="grant-expiry">
            <NativeSelect id="grant-expiry" v-model="expiry" data-testid="grant-expiry">
              <option v-for="e in EXPIRY_CHOICES" :key="e" :value="e">{{ e === "never" ? t("billing.credits.grant.never") : e === "date" ? t("billing.credits.grant.onDate") : t("billing.credits.grant.inDays", { n: Number(e) }) }}</option>
            </NativeSelect>
          </FormField>
          <FormField v-if="expiry === 'date'" :label="t('billing.credits.grant.expiry')" for="grant-date" :hint="t('billing.credits.grant.expiryHint')" :error="errs.date">
            <Input id="grant-date" v-model="date" type="date" :aria-invalid="errs.date ? true : undefined" aria-describedby="grant-date-error" />
          </FormField>
          <ErrorText :error="grant.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="grant-form" :disabled="grant.isPending.value" data-testid="grant-submit">{{ t("billing.credits.grant.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
