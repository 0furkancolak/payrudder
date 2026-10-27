<script setup lang="ts">
import { computed } from "vue";
import { useCustomers } from "./api";

/** A customer in a list: the name, with the external id under it (or the id alone while the names load or when the customer is not known by name). */
const props = defineProps<{ tenantId: string; id?: string }>();
const customers = useCustomers(() => props.tenantId);
const name = computed(() => (customers.data.value?.customers ?? []).find((c) => c.external_id === props.id)?.name ?? "");
</script>

<template>
  <template v-if="name && name !== id">
    <span>{{ name }}</span>
    <span class="text-muted-foreground mono block text-xs">{{ id }}</span>
  </template>
  <template v-else>{{ id }}</template>
</template>
