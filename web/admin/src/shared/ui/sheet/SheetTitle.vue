<script setup lang="ts">
import type { DialogTitleProps } from "reka-ui";
import type { HTMLAttributes } from "vue";
import { DialogTitle } from "reka-ui";
import { inject, onMounted, onUpdated, ref } from "vue";
import { setTitle } from "@/shared/lib/sheets";
import { cn } from "@/shared/lib/utils";
import { SHEET_CONTEXT } from "./context";

const props = defineProps<DialogTitleProps & { class?: HTMLAttributes["class"] }>();
// The title is the name of the sheet's tab in the strip of open sheets.
const sheet = inject(SHEET_CONTEXT, null);
const el = ref<{ $el?: HTMLElement } | null>(null);
const sync = () => sheet && setTitle(sheet.uid, el.value?.$el?.textContent?.trim() ?? "");
onMounted(sync);
onUpdated(sync);
</script>

<template>
  <DialogTitle ref="el" data-slot="sheet-title" v-bind="{ asChild: props.asChild, as: props.as }" :class="cn('text-lg leading-tight font-semibold', props.class)"><slot /></DialogTitle>
</template>
