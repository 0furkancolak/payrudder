<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Textarea } from "@/shared/ui/textarea";
import { useCannedReplies, useOperatorSupportActions, type CannedReply } from "./api";

/**
 * The operators' canned replies: titled texts every operator can paste into an answer. Made, changed and deleted here; a change is everybody's (it shows who saved
 * it last). Pasting one into an answer is the inbox's own picker.
 */
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const list = useCannedReplies();
const actions = useOperatorSupportActions();
const editing = ref<string>(""); // the id being changed; empty makes a new one
const title = ref("");
const body = ref("");
/** The reply whose delete is being asked about, in its own row: a second dialog over this one would sit under its overlay. */
const asking = ref("");

function edit(r?: CannedReply) {
  editing.value = r?.id ?? "";
  title.value = r?.title ?? "";
  body.value = r?.body ?? "";
  actions.saveCanned.reset();
}
function save() {
  if (title.value.trim() === "" || body.value.trim() === "") return;
  actions.saveCanned.mutate({ ...(editing.value ? { id: editing.value } : {}), title: title.value.trim(), body: body.value.trim() }, { onSuccess: () => edit() });
}
function remove(r: CannedReply) {
  asking.value = "";
  actions.deleteCanned.mutate(r.id, { onSuccess: () => (editing.value === r.id ? edit() : undefined) });
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-2xl" data-testid="canned-dialog">
      <DialogHeader>
        <DialogTitle>{{ t("support.canned.title") }}</DialogTitle>
        <DialogDescription>{{ t("support.canned.hint") }}</DialogDescription>
      </DialogHeader>
      <ul v-if="(list.data.value ?? []).length > 0" class="divide-border/70 m-0 flex max-h-56 list-none flex-col divide-y overflow-y-auto p-0 text-sm" data-testid="canned-list">
        <li v-for="r in list.data.value" :key="r.id" class="flex items-center justify-between gap-3 py-2" :data-testid="`canned-${r.id}`">
          <span class="min-w-0"><span class="font-medium">{{ r.title }}</span><span class="text-muted-foreground block truncate text-xs">{{ r.body }}</span></span>
          <span v-if="asking === r.id" class="flex shrink-0 items-center gap-2" role="group" :aria-label="t('support.canned.confirmDelete', { title: r.title })">
            <span class="text-xs">{{ t("support.canned.confirmDelete", { title: r.title }) }}</span>
            <Button type="button" size="sm" variant="destructive" :data-testid="`canned-delete-yes-${r.id}`" @click="remove(r)">{{ t("support.canned.delete") }}</Button>
            <Button type="button" size="sm" variant="ghost" @click="asking = ''">{{ t("common.cancel") }}</Button>
          </span>
          <span v-else class="flex shrink-0 gap-2">
            <Button type="button" size="sm" variant="outline" :data-testid="`canned-edit-${r.id}`" @click="edit(r)">{{ t("support.canned.edit") }}</Button>
            <Button type="button" size="sm" variant="ghost" :data-testid="`canned-delete-${r.id}`" @click="asking = r.id">{{ t("support.canned.delete") }}</Button>
          </span>
        </li>
      </ul>
      <p v-else-if="list.isSuccess.value" class="text-muted-foreground m-0 text-sm" data-testid="canned-empty">{{ t("support.canned.empty") }}</p>
      <form class="grid gap-2" @submit.prevent="save">
        <label class="grid gap-1 text-xs">{{ t("support.canned.fieldTitle") }}
          <Input v-model="title" maxlength="100" data-testid="canned-title" />
        </label>
        <label class="grid gap-1 text-xs">{{ t("support.canned.fieldBody") }}
          <Textarea v-model="body" rows="5" maxlength="10000" data-testid="canned-body" />
        </label>
        <ErrorText :error="actions.saveCanned.error.value ?? actions.deleteCanned.error.value ?? list.error.value" />
        <div class="flex flex-wrap justify-end gap-2">
          <Button v-if="editing" type="button" variant="ghost" data-testid="canned-cancel" @click="edit()">{{ t("common.cancel") }}</Button>
          <Button type="submit" :disabled="actions.saveCanned.isPending.value || title.trim() === '' || body.trim() === ''" data-testid="canned-save">{{ editing ? t("support.canned.save") : t("support.canned.add") }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
