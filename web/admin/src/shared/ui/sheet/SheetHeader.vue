<script setup lang="ts">
import type { HTMLAttributes } from "vue";
import { cn } from "@/shared/lib/utils";

const props = defineProps<{ class?: HTMLAttributes["class"] }>();
</script>

<template>
  <div data-slot="sheet-header" :class="cn('flex flex-col gap-1.5 border-b px-6 py-5 pe-14', props.class)"><slot /></div>
</template>
