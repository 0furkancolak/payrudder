<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import EInvoiceStatus from "@/features/einvoice/EInvoiceStatus.vue";
import { useCreateCreditNote, useCreditNotes, useRetryCreditNoteRefund } from "./api";
import { creditAmountMinor } from "./lib";

/** Credit notes of a tenant, or of one invoice; with `issuable` a paid invoice can be credited here. */
const props = defineProps<{ tenantId: string; invoiceId?: string; currency?: string; issuable?: boolean }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const notes = useCreditNotes(
  () => props.tenantId,
  () => props.invoiceId ?? "",
);
const create = useCreateCreditNote(() => props.tenantId);
const retry = useRetryCreditNoteRefund(() => props.tenantId);
const toCard = ref(false);
const amount = ref("");
const reason = ref("");
const formError = ref("");
const date = (v?: string) => fmt.date(v, "day", "—");
const list = computed(() => notes.data.value?.credit_notes ?? []);
/** Where a note's amount went. */
function where(n: { disposition?: string; refund_state?: string }) {
  if (n.disposition !== "CREDIT_NOTE_DISPOSITION_REFUND") return t("billing.creditNotes.toCredit");
  switch (n.refund_state) {
    case "REFUND_STATE_REFUNDED":
      return t("billing.creditNotes.refunded");
    case "REFUND_STATE_FAILED":
      return t("billing.creditNotes.refundFailed");
    default:
      return t("billing.creditNotes.refundRequested");
  }
}

function issue() {
  formError.value = "";
  const minor = creditAmountMinor(amount.value, props.currency);
  if (minor === null) {
    formError.value = t("billing.creditNotes.badAmount", { currency: props.currency ?? "" });
    return;
  }
  create.mutate(
    { invoiceId: props.invoiceId ?? "", amountMinor: minor, reason: reason.value.trim(), refundToCard: toCard.value },
    { onSuccess: () => (amount.value = reason.value = "") },
  );
}
</script>

<template>
  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.creditNotes.title") }}</h2>
    <form v-if="issuable" class="grid gap-3 px-3 sm:grid-cols-[1fr_2fr_auto_auto]" role="form" :aria-label="t('billing.creditNotes.formLabel')" @submit.prevent="issue">
      <Input v-model="amount" inputmode="decimal" :aria-label="t('billing.creditNotes.amount')" :placeholder="t('billing.creditNotes.amountPlaceholder')" />
      <Input v-model="reason" maxlength="500" :aria-label="t('billing.creditNotes.reason')" :placeholder="t('billing.creditNotes.reason')" />
      <label class="flex items-center gap-2 text-sm"><input v-model="toCard" type="checkbox" class="size-4" />{{ t("billing.creditNotes.refundToCard") }}</label>
      <Button type="submit" :disabled="create.isPending.value">{{ t("billing.creditNotes.create") }}</Button>
    </form>
    <ErrorText :message="formError" class="px-3" />
    <ErrorText :error="create.error.value" class="px-3" />
    <ErrorText :error="retry.error.value" class="px-3" />
    <ErrorText :error="notes.error.value" class="px-3" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.creditNotes.number") }}</TableHead>
          <TableHead v-if="!invoiceId">{{ t("billing.creditNotes.invoice") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.customer") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.reason") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.where") }}</TableHead>
          <TableHead>{{ t("einvoice.title") }}</TableHead>
          <TableHead>{{ t("billing.creditNotes.date") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.creditNotes.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="n in list" :key="n.id">
          <TableCell class="mono font-medium">{{ n.number }}</TableCell>
          <TableCell v-if="!invoiceId" class="mono">{{ n.invoice_number }}</TableCell>
          <TableCell>{{ n.customer_external_id }}</TableCell>
          <TableCell>{{ n.reason }}</TableCell>
          <TableCell>
            {{ where(n) }}
            <template v-if="n.refund_state === 'REFUND_STATE_FAILED'">
              <span v-if="n.refund_error" class="text-muted-foreground block text-xs">{{ n.refund_error }}</span>
              <Button size="sm" variant="outline" :disabled="retry.isPending.value" @click="retry.mutate(n.id ?? '')">{{ t("billing.creditNotes.retryRefund") }}</Button>
            </template>
          </TableCell>
          <TableCell><EInvoiceStatus :tenant-id="tenantId" :credit-note-id="n.id ?? ''" /></TableCell>
          <TableCell>{{ date(n.create_time) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(n.total_minor, n.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!notes.isPending.value && list.length === 0 && !notes.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.creditNotes.empty") }}</p>
  </Card>
</template>
