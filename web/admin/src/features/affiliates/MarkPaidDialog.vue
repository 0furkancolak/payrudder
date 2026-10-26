<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useMarkPaid } from "./api";
import type { Payout } from "./logic";

/** Records that the owner paid a payout. Payrudder moves no money: the owner pays by hand or bank transfer and says so here, which posts it to the books. */
const props = defineProps<{ tenantId: string; payout: Payout | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const fmt = useFormat();
const pay = useMarkPaid(() => props.tenantId);
const reference = ref("");

watch(open, (isOpen) => {
  if (!isOpen) return;
  reference.value = "";
  pay.reset();
});

function submit() {
  pay.mutate(
    { id: props.payout?.id ?? "", reference: reference.value.trim() },
    {
      onSuccess: () => {
        toast.success(t("affiliates.pay.done"));
        open.value = false;
      },
    },
  );
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("affiliates.pay.title") }}</SheetTitle>
        <SheetDescription>
          {{ t("affiliates.pay.summary", { code: payout?.affiliate_code ?? "", amount: fmt.amount(payout?.total_minor, payout?.currency) }) }}
        </SheetDescription>
      </SheetHeader>
      <SheetBody>
      <p v-if="payout?.fake" data-testid="fake-warning" role="alert" class="m-0 bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm font-medium">{{ t("affiliates.pay.fakeWarning") }}</p>
      <p class="bg-muted text-muted-foreground rounded-2xl px-4 py-3 text-sm">{{ t("affiliates.pay.noMoney") }}</p>
      <div v-if="payout?.payout_details" class="grid gap-1">
        <span class="text-sm font-medium">{{ t("affiliates.payoutDetails") }}</span>
        <p class="bg-muted/50 m-0 rounded-xl px-3 py-2 text-sm whitespace-pre-wrap" dir="auto">{{ payout.payout_details }}</p>
      </div>
      <form id="af-pay" class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="pay-ref">{{ t("affiliates.pay.reference") }}</Label>
          <Input id="pay-ref" v-model="reference" maxlength="200" autocomplete="off" dir="auto" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.pay.referenceHint") }}</p>
        </div>
        <ErrorText :error="pay.error.value" />
      </form>
      </SheetBody>
      <SheetFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.cancel") }}</Button>
          <Button type="submit" form="af-pay" :disabled="pay.isPending.value">{{ t("affiliates.pay.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
