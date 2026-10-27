<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { answerConfirm, pendingConfirm } from "@/shared/lib/confirm";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";

/** The one styled confirmation of a destructive action (see `askConfirm`). Mounted once, in the app shell. */
const { t } = useI18n();
const pending = computed(() => pendingConfirm.value);
const open = computed({
  get: () => pending.value !== null,
  set: (v) => {
    if (!v) answerConfirm(false);
  },
});
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md" data-testid="confirm-dialog">
      <DialogHeader>
        <DialogTitle>{{ pending?.title ?? t("common.confirm.title") }}</DialogTitle>
        <DialogDescription>{{ pending?.body }}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="ghost" data-testid="confirm-cancel" @click="answerConfirm(false)">{{ t("common.cancel") }}</Button>
        <Button :variant="pending?.destructive === false ? 'default' : 'destructive'" data-testid="confirm-accept" @click="answerConfirm(true)">{{ pending?.confirmLabel ?? t("common.confirm.accept") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
