<script setup lang="ts">
import type { HTMLAttributes } from "vue";
import { cn } from "@/shared/lib/utils";

const props = defineProps<{ class?: HTMLAttributes["class"] }>();
</script>

<template>
  <div data-slot="sheet-body" :class="cn('flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-6 py-5', props.class)"><slot /></div>
</template>
