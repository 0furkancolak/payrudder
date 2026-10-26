<script setup lang="ts">
import type { DialogTitleProps } from "reka-ui";
import type { HTMLAttributes } from "vue";
import { DialogTitle } from "reka-ui";
import { cn } from "@/shared/lib/utils";

const props = defineProps<DialogTitleProps & { class?: HTMLAttributes["class"] }>();
</script>

<template>
  <DialogTitle data-slot="sheet-title" v-bind="{ asChild: props.asChild, as: props.as }" :class="cn('text-lg leading-tight font-semibold', props.class)"><slot /></DialogTitle>
</template>
