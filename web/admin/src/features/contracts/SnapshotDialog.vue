<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { useContractSnapshot } from "./api";
import SandboxedHtml from "./SandboxedHtml.vue";

/**
 * The page a customer accepted, exactly as it was stored when they accepted it, with the hash the acceptance holds (the API checks the page against it
 * before sending it). The page is data the seller's customers' checkout produced, so it is shown in a sandboxed frame and never as markup of this page.
 * The model is the id of an acceptance of the group; empty is closed.
 */
const props = defineProps<{ tenantId: string }>();
const acceptance = defineModel<string>("acceptance", { required: true });
const { t } = useI18n();
const snapshot = useContractSnapshot(() => props.tenantId, acceptance);
const open = computed({
  get: () => acceptance.value !== "",
  set: (v) => {
    if (!v) acceptance.value = "";
  },
});
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="flex h-[85vh] flex-col sm:max-w-3xl" data-testid="snapshot-dialog">
      <DialogHeader>
        <DialogTitle>{{ t("contracts.customer.snapshotTitle") }}</DialogTitle>
        <DialogDescription>{{ t("contracts.customer.snapshotHint") }}</DialogDescription>
      </DialogHeader>
      <ErrorState :error="snapshot.error.value" :retrying="snapshot.isFetching.value" compact @retry="snapshot.refetch()" />
      <Skeleton v-if="snapshot.isPending.value && !snapshot.error.value" class="min-h-0 flex-1 rounded-xl" />
      <div v-else-if="snapshot.data.value?.html" class="min-h-0 flex-1">
        <SandboxedHtml :doc="snapshot.data.value.html" :title="t('contracts.customer.snapshotTitle')" />
      </div>
      <p v-if="snapshot.data.value?.sha256" class="text-muted-foreground m-0 text-xs break-all">
        {{ t("contracts.customer.sha256") }}: <span class="mono" data-testid="snapshot-sha256">{{ snapshot.data.value.sha256 }}</span>
      </p>
    </DialogContent>
  </Dialog>
</template>
