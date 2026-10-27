<script setup lang="ts">
import { Combobox, type ComboboxOption, type CustomOption } from "@/shared/ui/combobox";

/**
 * A select with a search box for a list the page already holds (the currencies, the time zones, the places of an address): a one-value Combobox over
 * static options. Without `custom` the value is always one of the options. With it, what was typed is offered too (a town the list does not have),
 * leaving the field takes the text, and a stored value that is no option still shows.
 */
export type SearchOption = ComboboxOption;

defineProps<{
  options: SearchOption[];
  placeholder?: string;
  emptyText?: string;
  id?: string;
  ariaLabel?: string;
  disabled?: boolean;
  invalid?: boolean;
  class?: string;
  /** Turns the typed text into an option of its own (or null when it is not acceptable): free text next to the list. */
  custom?: CustomOption;
}>();
const model = defineModel<string>({ required: true });
</script>

<template>
  <Combobox :id="id" v-model="model" :options="options" :placeholder="placeholder" :empty-text="emptyText" :aria-label="ariaLabel" :disabled="disabled" :invalid="invalid" :class="$props.class" :custom="custom" />
</template>
