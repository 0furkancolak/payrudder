<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAttemptKey } from "@/shared/lib/key";
import { sheetName } from "@/shared/lib/sheets";
import { CREDITS_RE } from "@/shared/lib/rules";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
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
const formError = ref("");
const valid = computed(() => props.customer !== "" && CREDITS_RE.test(credits.value.trim()));

// A sheet that was closed opens clean: the last values and error are not shown again.
watch(open, (v) => {
  if (!v) return;
  credits.value = note.value = date.value = formError.value = "";
  reason.value = "welcome";
  expiry.value = "never";
  grant.reset();
});

function submit() {
  formError.value = "";
  if (!valid.value) return;
  const expireTime = expiryTime(expiry.value, date.value);
  if (expireTime === undefined) {
    formError.value = t("billing.credits.grant.badExpiry");
    return;
  }
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
        <form id="grant-form" class="grid gap-4" role="form" :aria-label="t('billing.credits.grant.formLabel')" @submit.prevent="submit">
          <Input v-model="credits" inputmode="numeric" :aria-label="t('billing.credits.grant.credits')" :placeholder="t('billing.credits.grant.credits')" required data-testid="grant-credits" />
          <NativeSelect v-model="reason" :aria-label="t('billing.credits.grant.source')" data-testid="grant-reason">
            <option v-for="r in REASONS" :key="r" :value="r">{{ t(`billing.credits.grant.reasons.${r}`) }}</option>
          </NativeSelect>
          <Input v-model="note" maxlength="150" :aria-label="t('billing.credits.grant.reason')" :placeholder="t('billing.credits.grant.reason')" />
          <NativeSelect v-model="expiry" :aria-label="t('billing.credits.grant.expires')" data-testid="grant-expiry">
            <option v-for="e in EXPIRY_CHOICES" :key="e" :value="e">{{ e === "never" ? t("billing.credits.grant.never") : e === "date" ? t("billing.credits.grant.onDate") : t("billing.credits.grant.inDays", { n: Number(e) }) }}</option>
          </NativeSelect>
          <Input v-if="expiry === 'date'" v-model="date" type="date" :aria-label="t('billing.credits.grant.expiry')" :title="t('billing.credits.grant.expiryHint')" required />
          <ErrorText :message="formError" />
          <ErrorText :error="grant.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="grant-form" :disabled="grant.isPending.value || !valid" data-testid="grant-submit">{{ t("billing.credits.grant.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
