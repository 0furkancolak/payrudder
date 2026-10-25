<script setup lang="ts">
import { NativeSelect } from "@/shared/ui/native-select";
import type { Package } from "./api";

/** A list of packages (by code) to choose one from. `placeholder` is the first option; `lockPlaceholder` makes it unchoosable, `disabledCode` greys one package out (the current one). */
defineProps<{ packages: readonly Package[]; label: string; placeholder: string; lockPlaceholder?: boolean; required?: boolean; disabledCode?: string }>();
const model = defineModel<string>({ required: true });
</script>

<template>
  <NativeSelect v-model="model" :aria-label="label" :required="required">
    <option value="" :disabled="lockPlaceholder">{{ placeholder }}</option>
    <option v-for="p in packages" :key="p.plan.id" :value="p.plan.code" :disabled="!!disabledCode && p.plan.code === disabledCode">{{ p.plan.code }}</option>
  </NativeSelect>
</template>
