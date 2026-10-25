<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { useFormat } from "@/shared/lib/format";
import { toast } from "vue-sonner";
import { label } from "@/shared/lib/labels";
import { minorToMajor, parseMajor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { useInvoicePayments } from "./paymentApi";
import { offlineSettlement, PAYMENT_METHODS, receivedAtToIso, toLocalInput, transferSettlement, type PaymentMethod, type Settlement } from "./payment";

/**
 * "Record payment" for an invoice. When the invoice was charged by bank transfer (`transfer`) this is "Confirm bank transfer" and goes to the transfer
 * endpoint (the bank reference is then required); otherwise it records a payment received outside Quotify.
 */
const props = defineProps<{ tenantId: string; invoiceId: string; currency: string; outstandingMinor: string; transfer: boolean }>();
const open = defineModel<boolean>("open", { required: true });
const { t, te } = useI18n();
const fmt = useFormat();
const pay = useInvoicePayments(
  () => props.tenantId,
  () => props.invoiceId,
);

const amount = ref("");
const receivedAt = ref("");
const reference = ref("");
const note = ref("");
const method = ref<PaymentMethod>("bank_transfer");
const formError = ref("");

// Every time the dialog opens it starts fresh: the whole outstanding amount, now.
watch(open, (isOpen) => {
  if (!isOpen) return;
  amount.value = minorToMajor(props.outstandingMinor, props.currency);
  receivedAt.value = toLocalInput(new Date());
  reference.value = note.value = formError.value = "";
  method.value = "bank_transfer";
  pay.confirmTransfer.reset();
  pay.recordPayment.reset();
});

const error = () => firstError(pay.confirmTransfer, pay.recordPayment);
const pending = () => pay.confirmTransfer.isPending.value || pay.recordPayment.isPending.value;

function announce(s: Settlement) {
  const money = (m: string) => fmt.money(m, props.currency);
  let text = s.paid ? t("invoice.payment.toast.paid", { received: money(s.received) }) : t("invoice.payment.toast.partly", { received: money(s.received), outstanding: money(s.outstanding) });
  if (s.duplicate) text += " " + t("invoice.payment.toast.duplicate");
  toast.success(text);
  open.value = false;
}

function submit() {
  formError.value = "";
  const minor = parseMajor(amount.value, props.currency);
  if (minor === null || minor === "0") {
    formError.value = t("invoice.payment.badAmount", { currency: props.currency });
    return;
  }
  const at = receivedAtToIso(receivedAt.value, new Date());
  if (at === null) {
    formError.value = t("invoice.payment.badDate");
    return;
  }
  const ref_ = reference.value.trim();
  if (props.transfer) {
    const asNumber = Number(minor);
    if (!Number.isSafeInteger(asNumber)) {
      formError.value = t("invoice.payment.badAmount", { currency: props.currency });
      return;
    }
    if (ref_ === "") {
      formError.value = t("invoice.payment.needReference");
      return;
    }
    pay.confirmTransfer.mutate({ amountMinor: asNumber, currency: props.currency, receivedAt: at, bankReference: ref_, note: note.value.trim() }, { onSuccess: (r) => announce(transferSettlement(r)) });
    return;
  }
  pay.recordPayment.mutate(
    { method: method.value, amountMinor: minor, receivedAt: at, reference: ref_, note: note.value.trim() },
    { onSuccess: (r) => announce(offlineSettlement(r)) },
  );
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ transfer ? t("invoice.payment.confirmTransfer") : t("invoice.payment.record") }}</DialogTitle>
        <DialogDescription>{{ transfer ? t("invoice.payment.transferHint") : t("invoice.payment.offlineHint") }}</DialogDescription>
      </DialogHeader>
      <form class="grid gap-3" @submit.prevent="submit">
        <div v-if="!transfer" class="grid gap-1.5">
          <Label for="pay-method">{{ t("invoice.payment.method") }}</Label>
          <NativeSelect id="pay-method" v-model="method">
            <option v-for="m in PAYMENT_METHODS" :key="m" :value="m">{{ label(t, te, "paymentMethod", m) }}</option>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="pay-amount">{{ t("invoice.payment.amount", { currency }) }}</Label>
          <Input id="pay-amount" v-model="amount" inputmode="decimal" autocomplete="off" />
          <p class="text-muted-foreground text-xs">{{ t("invoice.payment.amountHint", { outstanding: fmt.money(outstandingMinor, currency) }) }}</p>
        </div>
        <div class="grid gap-1.5">
          <Label for="pay-date">{{ t("invoice.payment.receivedAt") }}</Label>
          <Input id="pay-date" v-model="receivedAt" type="datetime-local" />
        </div>
        <div class="grid gap-1.5">
          <Label for="pay-reference">{{ transfer ? t("invoice.payment.bankReference") : t("invoice.payment.reference") }}</Label>
          <Input id="pay-reference" v-model="reference" maxlength="128" autocomplete="off" />
          <p class="text-muted-foreground text-xs">{{ transfer ? t("invoice.payment.bankReferenceHint") : t("invoice.payment.referenceHint") }}</p>
        </div>
        <div class="grid gap-1.5">
          <Label for="pay-note">{{ t("invoice.payment.note") }}</Label>
          <Textarea id="pay-note" v-model="note" rows="2" maxlength="500" />
        </div>
        <ErrorText :message="formError" />
        <ErrorText :error="error()" />
        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("invoice.payment.cancel") }}</Button>
          <Button type="submit" :disabled="pending()">{{ transfer ? t("invoice.payment.confirmSubmit") : t("invoice.payment.submit") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
