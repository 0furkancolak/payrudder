<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { errorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useInvoicePayments } from "./paymentApi";

/** Gives up on a pending bank transfer: it was refused (`rejected`) or its time is up (`expired`). The tenant's dunning schedule then applies. */
const props = defineProps<{ tenantId: string; invoiceId: string; outcome: "rejected" | "expired" }>();
const open = defineModel<boolean>("open", { required: true });
const { t, te } = useI18n();
const pay = useInvoicePayments(
  () => props.tenantId,
  () => props.invoiceId,
);
const reason = ref("");
watch(open, (isOpen) => {
  if (!isOpen) return;
  reason.value = "";
  pay.rejectTransfer.reset();
});

function confirm() {
  pay.rejectTransfer.mutate(
    { outcome: props.outcome, reason: reason.value.trim() },
    {
      onSuccess: () => {
        toast.success(t(props.outcome === "expired" ? "invoice.transfer.toast.expired" : "invoice.transfer.toast.rejected"));
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ outcome === "expired" ? t("invoice.transfer.expireTitle") : t("invoice.transfer.rejectTitle") }}</DialogTitle>
        <DialogDescription>{{ outcome === "expired" ? t("invoice.transfer.expireHint") : t("invoice.transfer.rejectHint") }}</DialogDescription>
      </DialogHeader>
      <form class="grid gap-3" @submit.prevent="confirm">
        <div class="grid gap-1.5">
          <Label for="reject-reason">{{ t("invoice.transfer.reason") }}</Label>
          <Input id="reject-reason" v-model="reason" maxlength="500" autocomplete="off" />
        </div>
        <p v-if="pay.rejectTransfer.error.value" class="text-destructive text-sm" role="alert">{{ errorText(t, te, pay.rejectTransfer.error.value) }}</p>
        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("invoice.payment.cancel") }}</Button>
          <Button type="submit" variant="destructive" :disabled="pay.rejectTransfer.isPending.value">{{ outcome === "expired" ? t("invoice.transfer.expire") : t("invoice.transfer.reject") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
