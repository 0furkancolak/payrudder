<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { sheetName } from "@/shared/lib/sheets";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCreateCreditNote } from "./api";
import { creditAmountMinor } from "./lib";

/** Credits a paid invoice. An empty amount credits what is left of the invoice; the amount can be sent back to the card instead of the credit balance. */
const props = defineProps<{ tenantId: string; invoiceId: string; currency?: string }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const create = useCreateCreditNote(() => props.tenantId);
const toCard = ref(false);
const amount = ref("");
const reason = ref("");
const formError = ref("");

watch(open, (isOpen) => {
  if (!isOpen) return;
  toCard.value = false;
  amount.value = reason.value = formError.value = "";
  create.reset();
});

function issue() {
  formError.value = "";
  const minor = creditAmountMinor(amount.value, props.currency);
  if (minor === null) {
    formError.value = t("billing.creditNotes.badAmount", { currency: props.currency ?? "" });
    return;
  }
  create.mutate({ invoiceId: props.invoiceId, amountMinor: minor, reason: reason.value.trim(), refundToCard: toCard.value }, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Sheet v-model:open="open" :name="invoiceId ? sheetName('issue-credit-note', invoiceId) : undefined">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.creditNotes.create") }}</SheetTitle>
        <SheetDescription>{{ t("billing.creditNotes.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="credit-note-form" class="grid gap-3" role="form" :aria-label="t('billing.creditNotes.formLabel')" @submit.prevent="issue">
          <div class="grid gap-1.5">
            <Label for="cn-amount">{{ t("billing.creditNotes.amount") }}</Label>
            <Input id="cn-amount" v-model="amount" inputmode="decimal" :aria-label="t('billing.creditNotes.amount')" :placeholder="t('billing.creditNotes.amountPlaceholder')" />
          </div>
          <div class="grid gap-1.5">
            <Label for="cn-reason">{{ t("billing.creditNotes.reason") }}</Label>
            <Input id="cn-reason" v-model="reason" maxlength="500" :aria-label="t('billing.creditNotes.reason')" :placeholder="t('billing.creditNotes.reason')" />
          </div>
          <label class="flex items-center gap-2 text-sm"><input v-model="toCard" type="checkbox" class="size-4" />{{ t("billing.creditNotes.refundToCard") }}</label>
          <ErrorText :message="formError" />
          <ErrorText :error="create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="outline" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="credit-note-form" :disabled="create.isPending.value">{{ t("billing.creditNotes.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
