<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useFormat } from "@/shared/lib/format";
import { parseMajor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { useReverseCommission, type ReverseInput } from "./api";
import type { Commission } from "./logic";

/**
 * Takes a commission back for a chargeback, or by hand. Refunds, voids and credit notes reverse on their own, so this is only for what Payrudder cannot
 * see. A commission that was already paid is netted from the partner's next payout.
 */
const props = defineProps<{ tenantId: string; commission: Commission | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const fmt = useFormat();
const reverse = useReverseCommission(() => props.tenantId);

const reason = ref<"chargeback" | "manual">("chargeback");
const amount = ref("");
const reference = ref("");
const note = ref("");
const problem = ref("");

const currency = computed(() => props.commission?.currency ?? "");
const left = computed(() => {
  const c = props.commission;
  if (!c?.amount_minor || !/^-?\d+$/.test(c.amount_minor)) return "";
  return String(BigInt(c.amount_minor) - BigInt(c.reversed_minor && /^\d+$/.test(c.reversed_minor) ? c.reversed_minor : "0"));
});

watch(open, (isOpen) => {
  if (!isOpen) return;
  reason.value = "chargeback";
  amount.value = reference.value = note.value = problem.value = "";
  reverse.reset();
});

function submit() {
  problem.value = "";
  const c = props.commission;
  if (!c?.invoice_id) return;
  const body: ReverseInput = { invoice_id: c.invoice_id, reason: reason.value };
  if (amount.value.trim() !== "") {
    const minor = parseMajor(amount.value, currency.value);
    if (minor === null || minor === "0" || (left.value !== "" && BigInt(minor) > BigInt(left.value))) {
      problem.value = t("affiliates.reverse.badAmount", { currency: currency.value });
      return;
    }
    body.amount_minor = minor;
  }
  if (reason.value === "manual" && reference.value.trim() === "") {
    problem.value = t("affiliates.reverse.referenceRequired");
    return;
  }
  if (reference.value.trim()) body.reference = reference.value.trim();
  if (note.value.trim()) body.note = note.value.trim();
  reverse.mutate(body, {
    onSuccess: (r) => {
      const took = r.reversed_minor && r.reversed_minor !== "0";
      if (took) toast.success(t("affiliates.reverse.done", { amount: fmt.amount(r.reversed_minor, currency.value) }));
      else toast.info(t("affiliates.reverse.nothing"));
      open.value = false;
    },
  });
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ t("affiliates.reverse.title") }}</DialogTitle>
        <DialogDescription>{{ t("affiliates.reverse.hint") }}</DialogDescription>
      </DialogHeader>
      <p class="bg-muted text-muted-foreground rounded-2xl px-4 py-3 text-sm">{{ t("affiliates.reverse.auto") }}</p>
      <dl class="m-0 grid grid-cols-2 gap-2 text-sm">
        <div>
          <dt class="text-muted-foreground">{{ t("affiliates.commissions.invoice") }}</dt>
          <dd class="m-0 font-medium">{{ commission?.invoice_number || commission?.invoice_id }}</dd>
        </div>
        <div>
          <dt class="text-muted-foreground">{{ t("affiliates.commissions.partner") }}</dt>
          <dd class="mono m-0 font-medium">{{ commission?.affiliate_code }}</dd>
        </div>
        <div>
          <dt class="text-muted-foreground">{{ t("affiliates.commissions.amount") }}</dt>
          <dd class="m-0 font-medium tabular-nums">{{ fmt.amount(commission?.amount_minor, currency) }}</dd>
        </div>
        <div>
          <dt class="text-muted-foreground">{{ t("affiliates.reverse.left") }}</dt>
          <dd class="m-0 font-medium tabular-nums" data-testid="reverse-left">{{ fmt.amount(left, currency) }}</dd>
        </div>
      </dl>
      <form class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="rv-reason">{{ t("affiliates.reverse.reason") }}</Label>
          <NativeSelect id="rv-reason" v-model="reason">
            <option value="chargeback">{{ t("affiliates.reverse.reasons.chargeback") }}</option>
            <option value="manual">{{ t("affiliates.reverse.reasons.manual") }}</option>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="rv-amount">{{ t("affiliates.reverse.amount", { currency }) }}</Label>
          <Input id="rv-amount" v-model="amount" inputmode="decimal" autocomplete="off" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.reverse.amountHint") }}</p>
        </div>
        <div class="grid gap-1.5">
          <Label for="rv-ref">{{ reason === "manual" ? t("affiliates.reverse.referenceRequiredLabel") : t("affiliates.reverse.reference") }}</Label>
          <Input id="rv-ref" v-model="reference" maxlength="100" autocomplete="off" :aria-required="reason === 'manual'" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.reverse.referenceHint") }}</p>
        </div>
        <div class="grid gap-1.5">
          <Label for="rv-note">{{ t("affiliates.reverse.note") }}</Label>
          <Textarea id="rv-note" v-model="note" rows="2" maxlength="500" />
        </div>
        <p class="text-muted-foreground text-xs">{{ t("affiliates.reverse.paidNote") }}</p>
        <ErrorText :message="problem" />
        <ErrorText :error="reverse.error.value" />
        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.cancel") }}</Button>
          <Button type="submit" variant="destructive" :disabled="reverse.isPending.value">{{ t("affiliates.reverse.submit") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
