<script setup lang="ts">
import { cn } from "@/shared/lib/utils";

/** Two to five options as one pill (it wraps on a narrow screen): a radio group underneath, so the arrow keys and screen readers work as they should. */
defineProps<{ options: { value: string; label: string }[]; label: string }>();
const model = defineModel<string>({ required: true });
</script>

<template>
  <div role="radiogroup" :aria-label="label" class="bg-muted inline-flex max-w-full flex-wrap rounded-xl p-1">
    <label
      v-for="o in options"
      :key="o.value"
      :class="
        cn(
          'focus-within:ring-ring/60 relative cursor-pointer rounded-lg px-4 py-1.5 text-sm font-medium transition-colors focus-within:ring-2',
          model === o.value ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
        )
      "
    >
      <input v-model="model" type="radio" class="sr-only" :value="o.value" :name="label" />
      {{ o.label }}
    </label>
  </div>
</template>
