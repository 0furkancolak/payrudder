<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, type Component } from "vue";
import { registerHeaderAction } from "./headerActions";

/**
 * A page's main button (new customer, add webhook, ...). It draws nothing where it is written: the header of the frame (HeaderActions.vue) draws it
 * between the breadcrumb and the tour / privacy / environment controls, an icon only on a phone. It can sit anywhere in the page's template.
 */
const props = defineProps<{ label: string; icon?: Component; disabled?: boolean; title?: string; testId?: string }>();
const emit = defineEmits<{ click: [] }>();
let off: (() => void) | undefined;
let gone = false;
// After the page has finished mounting: the header redraws on registration, and doing that while the router's outlet is half-mounted breaks Vue's patch.
onMounted(() =>
  void nextTick(() => {
    if (!gone) off = registerHeaderAction({ props, click: () => emit("click") });
  }),
);
onBeforeUnmount(() => {
  gone = true;
  off?.();
});
</script>

<template><span hidden /></template>
