<script setup lang="ts">
import { computed } from "vue";
import { NativeSelect } from "@/shared/ui/native-select";
import { useCustomers } from "./api";

/**
 * A list of the tenant's customers to choose one from; the value is the customer's external id. `named` shows "Name (external id)", otherwise the id alone.
 * `placeholder` is the first, empty option (`lockPlaceholder` makes it unchoosable once something else is picked).
 */
const props = defineProps<{ tenantId: string; placeholder: string; label: string; named?: boolean; required?: boolean; lockPlaceholder?: boolean }>();
const model = defineModel<string>({ required: true });
const customers = useCustomers(() => props.tenantId);
const list = computed(() => customers.data.value?.customers ?? []);
</script>

<template>
  <NativeSelect v-model="model" :aria-label="label" :required="required">
    <option value="" :disabled="lockPlaceholder">{{ placeholder }}</option>
    <option v-for="c in list" :key="c.id" :value="c.external_id">{{ named ? `${c.name} (${c.external_id})` : c.external_id }}</option>
  </NativeSelect>
</template>
