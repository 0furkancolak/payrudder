<script setup lang="ts">
import { computed } from "vue";
import { cn } from "@/shared/lib/utils";

/**
 * A status as a pill: a dot and a word on a soft tint. The colour is never the only signal (the word is always there), and each tint keeps
 * the text above 4.5:1 on its background in both themes.
 */
const props = defineProps<{ tone: "success" | "info" | "warning" | "danger" | "neutral"; class?: string }>();

const tones = {
  success: "bg-success-soft text-success",
  info: "bg-brand-soft text-brand-strong",
  warning: "bg-warning-soft text-warning",
  danger: "bg-destructive-soft text-destructive",
  neutral: "bg-muted text-muted-foreground",
} as const;
const cls = computed(() => cn("inline-flex w-fit shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium whitespace-nowrap", tones[props.tone], props.class));
</script>

<template>
  <span :class="cls"><span class="size-1.5 rounded-full bg-current" aria-hidden="true" /><slot /></span>
</template>
