<script setup lang="ts">
import type { DialogRootEmits, DialogRootProps } from "reka-ui";
import { reactiveOmit } from "@vueuse/core";
import { DialogRoot, useForwardPropsEmits } from "reka-ui";
import { computed, onBeforeUnmount, provide, watch } from "vue";
import { closeName, enter, frontUid, leave, names, newSheetUid, openName } from "@/shared/lib/sheets";
import { SHEET_CONTEXT } from "./context";

/**
 * A sheet. Sheets stack: opening one while another is open puts it in front, and every open sheet is a tab of the strip at the top of the one in
 * front (see shared/lib/sheets). A sheet with a `name` is written into the address while it is open and opens by itself when the address names it,
 * so a reload or a shared link brings it back. The overlay behind the stack is drawn by the content, since the dialog is not modal: only so can a
 * sheet further back be raised and used.
 */
const props = defineProps<DialogRootProps & { name?: string }>();
const emits = defineEmits<DialogRootEmits>();
const forwarded = useForwardPropsEmits(reactiveOmit(props, "name"), emits);

const uid = newSheetUid();
const isOpen = computed(() => props.open === true);
provide(SHEET_CONTEXT, { uid, open: isOpen });

const close = () => emits("update:open", false);
watch(
  isOpen,
  (o, was) => {
    // First look at a sheet that starts closed: the address may be naming it (the watcher below opens it), so nothing is taken off the stack.
    if (!o && was === undefined) return;
    if (o) {
      enter({ uid, title: "", name: props.name, close });
      if (props.name) openName(props.name);
    } else {
      leave(uid);
      if (props.name) closeName(props.name);
    }
  },
  { immediate: true },
);
// The address names this sheet: open it; the address no longer names it: close it.
watch(
  () => !!props.name && names.value.includes(props.name),
  (named) => {
    if (named && !isOpen.value) emits("update:open", true);
    else if (!named && isOpen.value && props.name) emits("update:open", false);
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  leave(uid);
  if (props.name && isOpen.value) closeName(props.name);
});
defineExpose({ front: computed(() => frontUid.value === uid) });
</script>

<template>
  <DialogRoot v-slot="slotProps" data-slot="sheet" v-bind="forwarded" :modal="false">
    <slot v-bind="slotProps" />
  </DialogRoot>
</template>
