<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
import { useOperatorSupportActions, type SupportTicket } from "./api";

/**
 * Merges the open ticket into another ticket of the same person (the help desk refuses any other pair): its messages and files move there and it is closed with a
 * pointer. The choice is the other tickets of the same person and organisation in the list the inbox has loaded (clear its status filter to see more), not merged
 * already and not closed.
 */
const props = defineProps<{ ticket: SupportTicket; candidates: readonly SupportTicket[] }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const actions = useOperatorSupportActions();
const into = ref("");
const options = computed(() => props.candidates.filter((c) => c.id !== props.ticket.id && c.opened_by === props.ticket.opened_by && c.account_id === props.ticket.account_id && !c.merged_into_id && c.status !== "closed"));
watch(open, (isOpen) => {
  if (!isOpen) return;
  into.value = options.value[0]?.id ?? "";
  actions.merge.reset();
});
function merge() {
  if (!into.value) return;
  actions.merge.mutate({ id: props.ticket.id, into: into.value }, { onSuccess: () => (open.value = false) });
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent data-testid="merge-dialog">
      <DialogHeader>
        <DialogTitle>{{ t("support.merge.title", { number: ticket.number }) }}</DialogTitle>
        <DialogDescription>{{ t("support.merge.hint") }}</DialogDescription>
      </DialogHeader>
      <label v-if="options.length > 0" class="grid gap-1 text-sm">{{ t("support.merge.into") }}
        <NativeSelect v-model="into" data-testid="merge-into">
          <option v-for="o in options" :key="o.id" :value="o.id">#{{ o.number }} · {{ o.subject }}</option>
        </NativeSelect>
      </label>
      <p v-else class="text-muted-foreground m-0 text-sm" data-testid="merge-none">{{ t("support.merge.none") }}</p>
      <ErrorText :error="actions.merge.error.value" />
      <DialogFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="button" :disabled="!into || actions.merge.isPending.value" data-testid="merge-confirm" @click="merge">{{ t("support.merge.confirm") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
