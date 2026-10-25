<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { Textarea } from "@/shared/ui/textarea";
import { useUpdateAffiliate } from "./api";
import type { Affiliate } from "./logic";

/** Changes a partner's name and payout details (what the payout asks for when it is paid by hand). */
const props = defineProps<{ tenantId: string; affiliate: Affiliate | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const update = useUpdateAffiliate(() => props.tenantId);

const name = ref("");
const method = ref<"manual" | "bank_transfer">("manual");
const details = ref("");

watch(open, (isOpen) => {
  if (!isOpen) return;
  name.value = props.affiliate?.name ?? "";
  method.value = props.affiliate?.payout_method ?? "manual";
  details.value = props.affiliate?.payout_details ?? "";
  update.reset();
});

function submit() {
  update.mutate(
    { id: props.affiliate?.id ?? "", body: { name: name.value.trim(), payout_method: method.value, payout_details: details.value.trim() } },
    {
      onSuccess: () => {
        toast.success(t("affiliates.edit.done"));
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
        <DialogTitle>{{ t("affiliates.edit.title", { code: affiliate?.code ?? "" }) }}</DialogTitle>
        <DialogDescription>{{ affiliate?.email }}</DialogDescription>
      </DialogHeader>
      <form class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="ae-name">{{ t("affiliates.add.name") }}</Label>
          <Input id="ae-name" v-model="name" autocomplete="off" maxlength="120" />
        </div>
        <div class="grid gap-1.5">
          <Label for="ae-method">{{ t("affiliates.payoutMethod") }}</Label>
          <NativeSelect id="ae-method" v-model="method">
            <option value="manual">{{ t("affiliates.methods.manual") }}</option>
            <option value="bank_transfer">{{ t("affiliates.methods.bank_transfer") }}</option>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="ae-details">{{ t("affiliates.payoutDetails") }}</Label>
          <Textarea id="ae-details" v-model="details" rows="4" maxlength="1000" />
          <p class="text-muted-foreground text-xs">{{ t("affiliates.payoutDetailsHint") }}</p>
        </div>
        <ErrorText :error="update.error.value" />
        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("affiliates.common.cancel") }}</Button>
          <Button type="submit" :disabled="update.isPending.value">{{ t("affiliates.common.save") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
