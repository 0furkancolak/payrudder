<script setup lang="ts">
import { computed } from "vue";
import { Combobox } from "@/shared/ui/combobox";
import { useCustomers } from "./api";

/**
 * The tenant's customers to choose one from, searchable by id, name and e-mail; the value is the customer's external id. `named` shows "Name (external id)",
 * otherwise the id alone. `placeholder` is what the empty field says; unless `lockPlaceholder` an empty choice (the placeholder itself) clears it again.
 */
const props = defineProps<{ id?: string; invalid?: boolean; tenantId: string; placeholder: string; label: string; named?: boolean; required?: boolean; lockPlaceholder?: boolean }>();
const model = defineModel<string>({ required: true });
const customers = useCustomers(() => props.tenantId);
const options = computed(() => {
  const list = (customers.data.value?.customers ?? [])
    .filter((c) => c.external_id)
    .map((c) => ({ value: c.external_id ?? "", label: props.named ? `${c.name} (${c.external_id})` : (c.external_id ?? ""), short: c.external_id ?? "", keywords: `${c.name ?? ""} ${c.email ?? ""}` }));
  return props.lockPlaceholder ? list : [{ value: "", label: props.placeholder }, ...list];
});
</script>

<template>
  <Combobox :id="id" v-model="model" :invalid="invalid" :options="options" :aria-label="label" :placeholder="placeholder" :required="required" />
</template>
