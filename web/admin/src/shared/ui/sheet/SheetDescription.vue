<script setup lang="ts">
import type { DialogDescriptionProps } from "reka-ui";
import type { HTMLAttributes } from "vue";
import { DialogDescription } from "reka-ui";
import { cn } from "@/shared/lib/utils";

const props = defineProps<DialogDescriptionProps & { class?: HTMLAttributes["class"] }>();
</script>

<template>
  <DialogDescription data-slot="sheet-description" v-bind="{ asChild: props.asChild, as: props.as }" :class="cn('text-muted-foreground text-sm', props.class)"><slot /></DialogDescription>
</template>
