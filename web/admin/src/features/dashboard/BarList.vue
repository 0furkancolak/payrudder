<script setup lang="ts">
import { computed } from "vue";
import { share } from "./lib";

/** A ranked list: a label, a bar sized against the largest value, and the value as text. The bar is decoration; the order and the text carry the meaning. */
const props = defineProps<{ items: { key: string; label: string; sub?: string; value: number; text: string }[]; tone?: "brand" | "warning" | "danger" }>();
const max = computed(() => Math.max(0, ...props.items.map((i) => i.value)));
const color = computed(() => (props.tone === "danger" ? "bg-destructive/70" : props.tone === "warning" ? "bg-warning/60" : "bg-brand"));
</script>

<template>
  <ol class="flex flex-col gap-3.5">
    <li v-for="(it, i) in items" :key="it.key" class="flex flex-col gap-1.5">
      <div class="flex items-baseline justify-between gap-3 text-sm">
        <span class="flex min-w-0 items-baseline gap-2">
          <span class="text-muted-foreground w-4 shrink-0 text-xs tabular-nums">{{ i + 1 }}</span>
          <span class="truncate font-medium">{{ it.label }}</span>
        </span>
        <span class="shrink-0 font-medium tabular-nums">{{ it.text }}</span>
      </div>
      <div class="bg-muted ms-6 h-2 overflow-hidden rounded-full" aria-hidden="true">
        <div :class="['h-full rounded-full', color]" :style="{ width: `${share(it.value, max)}%` }" />
      </div>
      <p v-if="it.sub" class="text-muted-foreground ms-6 truncate text-xs">{{ it.sub }}</p>
    </li>
  </ol>
</template>
