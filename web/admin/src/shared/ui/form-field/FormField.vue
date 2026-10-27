<script setup lang="ts">
import { Label } from "@/shared/ui/label";

/** A label above its control (the default slot), the way every form of the console stacks them. `for` is the id of the control. */
defineProps<{ label: string; for?: string }>();
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <Label :for="$props.for">{{ label }}</Label>
    <slot />
  </div>
</template>
