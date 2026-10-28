<script setup lang="ts">
import { Checkbox } from "@/shared/ui/checkbox";

/** The tick of a row (or, with `head`, of the table's header, which ticks the page). Clicks on it never open the row's sheet. */
defineProps<{ label: string; checked: boolean; indeterminate?: boolean }>();
defineEmits<{ change: [on: boolean] }>();
</script>

<template>
  <span class="inline-flex" @click.stop>
    <Checkbox :model-value="indeterminate ? 'indeterminate' : checked" :aria-label="label" data-testid="row-check" @update:model-value="(v: boolean | 'indeterminate') => $emit('change', v === true)" />
  </span>
</template>
