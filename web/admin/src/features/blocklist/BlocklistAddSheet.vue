<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Textarea } from "@/shared/ui/textarea";
import { KINDS, type BlocklistKind, type NewEntry } from "./api";

/** The form that puts a value on a blocklist: what it is, the value and the owner's own note (never shown to the person). `add` is the mutation of the list the page is about. */
const props = defineProps<{ platform?: boolean; pending: boolean; error: unknown }>();
const emit = defineEmits<{ add: [entry: NewEntry] }>();
const open = defineModel<boolean>("open", { default: false });
const { t } = useI18n();
const kind = ref<BlocklistKind>("email");
const value = ref("");
const note = ref("");
watch(open, (v) => {
  if (v) [kind.value, value.value, note.value] = ["email", "", ""];
});
const ready = () => value.value.trim() !== "";
function save() {
  if (ready()) emit("add", { kind: kind.value, value: value.value.trim(), note: note.value.trim() });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("blocklist.add.title") }}</SheetTitle>
        <SheetDescription>{{ props.platform ? t("blocklist.add.descriptionPlatform") : t("blocklist.add.description") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="blocklist-add" class="grid gap-4" @submit.prevent="save">
          <FormField :label="t('blocklist.add.kind')" for="blocklist-kind">
            <NativeSelect id="blocklist-kind" v-model="kind" data-testid="blocklist-kind">
              <option v-for="k in KINDS" :key="k" :value="k">{{ t(`blocklist.kinds.${k}`) }}</option>
            </NativeSelect>
          </FormField>
          <FormField :label="t('blocklist.add.value')" for="blocklist-value" :hint="t(`blocklist.add.valueHint.${kind}`)">
            <Input id="blocklist-value" v-model="value" maxlength="320" autocomplete="off" dir="auto" data-testid="blocklist-value" />
          </FormField>
          <FormField :label="t('blocklist.add.note')" for="blocklist-note" :hint="t('blocklist.add.noteHint')">
            <Textarea id="blocklist-note" v-model="note" maxlength="500" data-testid="blocklist-note" />
          </FormField>
          <ErrorText :error="error" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("billing.customers.address.cancel") }}</Button>
        <Button type="submit" form="blocklist-add" :disabled="pending || !ready()" data-testid="blocklist-submit">{{ t("blocklist.add.submit") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
