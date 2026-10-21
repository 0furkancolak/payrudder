<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { useVoidExpense, type Expense } from "./api";

/** Voids an expense. It is not deleted: it leaves every summary, and the reason stays with it. */
const props = defineProps<{ tenantId: string; expense: Expense | null }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const errorText = useErrorText();
const voidIt = useVoidExpense(() => props.tenantId);
const reason = ref("");

watch(open, (isOpen) => {
  if (!isOpen) return;
  reason.value = "";
  voidIt.reset();
});

function submit() {
  if (!props.expense?.id || reason.value.trim() === "") return;
  voidIt.mutate({ id: props.expense.id, reason: reason.value.trim() }, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ t("billing.accounting.void.title", { vendor: expense?.vendor ?? "" }) }}</DialogTitle>
        <DialogDescription>{{ t("billing.accounting.void.hint") }}</DialogDescription>
      </DialogHeader>
      <form class="grid gap-3" @submit.prevent="submit">
        <div class="grid gap-1.5">
          <Label for="void-reason">{{ t("billing.accounting.void.reason") }}</Label>
          <Input id="void-reason" v-model="reason" maxlength="200" autocomplete="off" />
        </div>
        <p v-if="voidIt.error.value" class="text-destructive text-sm" role="alert">{{ errorText(voidIt.error.value) }}</p>
        <DialogFooter>
          <Button type="button" variant="outline" @click="open = false">{{ t("billing.accounting.form.cancel") }}</Button>
          <Button type="submit" variant="destructive" :disabled="voidIt.isPending.value || reason.trim() === ''">{{ t("billing.accounting.void.submit") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
