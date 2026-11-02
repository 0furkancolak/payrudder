<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { askConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Textarea } from "@/shared/ui/textarea";
import { useResolveCreditNoteRefund } from "./api";

/** The credit note whose failed refund is being settled. */
export interface FailedRefund {
  id?: string;
  number?: string;
  total_minor?: string;
  currency?: string;
  refund_error?: string;
}

/**
 * Settles a refund that failed, above all one whose outcome is unknown. The provider is never asked again under the same key (iyzico and Paddle do not dedupe a
 * refund), so the operator checks at the provider and says what they found: the customer was paid back (the reference is recorded, the note becomes refunded) or was
 * not (the refund is asked again under a new key). Either is confirmed first, and written to the audit log. The model holds the note being settled (null is closed).
 */
const props = defineProps<{ tenantId: string }>();
const note = defineModel<FailedRefund | null>("note", { default: null });
const { t, locale } = useI18n();
const resolve = useResolveCreditNoteRefund(() => props.tenantId);
const outcome = ref<"refunded" | "not_refunded">("refunded");
const refundId = ref("");
const what = ref("");
const open = computed({ get: () => note.value !== null, set: (v) => !v && (note.value = null) });
const options = computed(() => [
  { value: "refunded", label: t("billing.creditNotes.resolve.refunded") },
  { value: "not_refunded", label: t("billing.creditNotes.resolve.notRefunded") },
]);
watch(note, (n) => {
  if (!n) return;
  outcome.value = "refunded";
  refundId.value = what.value = "";
  resolve.reset();
});

async function save() {
  const n = note.value;
  if (!n?.id) return;
  const refunded = outcome.value === "refunded";
  const amount = formatMinor(n.total_minor, n.currency, locale.value);
  const ok = await askConfirm({
    body: refunded ? t("billing.creditNotes.resolve.confirmRefunded", { number: n.number ?? "", amount, ref: refundId.value.trim() }) : t("billing.creditNotes.resolve.confirmNotRefunded", { number: n.number ?? "", amount }),
    confirmLabel: refunded ? t("billing.creditNotes.resolve.submitRefunded") : t("billing.creditNotes.resolve.submitNotRefunded"),
    destructive: !refunded,
  });
  if (!ok) return;
  resolve.mutate({ creditNoteId: n.id, refunded, providerRefundId: refundId.value.trim(), note: what.value.trim() }, { onSuccess: () => (note.value = null) });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.creditNotes.resolve.title", { number: note?.number ?? "" }) }}</SheetTitle>
        <SheetDescription>{{ t("billing.creditNotes.resolve.description") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="resolve-refund" class="grid gap-4" role="form" :aria-label="t('billing.creditNotes.resolve.formLabel')" @submit.prevent="save">
          <p v-if="note?.refund_error" class="text-muted-foreground text-sm">{{ note.refund_error }}</p>
          <SegmentedControl v-model="outcome" :options="options" :label="t('billing.creditNotes.resolve.found')" />
          <FormField v-if="outcome === 'refunded'" :label="t('billing.creditNotes.resolve.refundId')" for="resolve-ref" :hint="t('billing.creditNotes.resolve.refundIdHint')">
            <Input id="resolve-ref" v-model="refundId" required maxlength="200" data-testid="resolve-ref" />
          </FormField>
          <p v-else class="text-sm">{{ t("billing.creditNotes.resolve.notRefundedHint") }}</p>
          <FormField :label="t('billing.creditNotes.resolve.note')" for="resolve-note" :hint="t('billing.creditNotes.resolve.noteHint')">
            <Textarea id="resolve-note" v-model="what" maxlength="500" data-testid="resolve-note" />
          </FormField>
          <ErrorText :error="resolve.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="note = null">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="resolve-refund" :disabled="resolve.isPending.value" data-testid="resolve-submit">
          {{ outcome === "refunded" ? t("billing.creditNotes.resolve.submitRefunded") : t("billing.creditNotes.resolve.submitNotRefunded") }}
        </Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
