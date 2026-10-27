<script setup lang="ts">
import { Label } from "@/shared/ui/label";

/**
 * A visible label above its control (the default slot), the way every form of the console stacks them. `for` is the id of the control. `error` is the
 * message of a failed check, shown right under the control (give the control `:aria-invalid` and `aria-describedby="<for>-error"`); `hint` is quiet help.
 * A placeholder is an example, never the label: it disappears once typed.
 */
defineProps<{ label: string; for?: string; error?: string; hint?: string }>();
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <Label :for="$props.for">{{ label }}</Label>
    <slot />
    <p v-if="hint && !error" class="text-muted-foreground text-xs">{{ hint }}</p>
    <p v-if="error" :id="$props.for ? `${$props.for}-error` : undefined" class="text-destructive text-xs" role="alert">{{ error }}</p>
  </div>
</template>
