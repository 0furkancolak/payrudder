<script setup lang="ts">
import { computed } from "vue";
import { useCustomers } from "@/features/customers/api";
import { maskEmail } from "@/shared/lib/money";
import { peek } from "./peek";

/** A customer by their external id: name, id and e-mail when the customer list has them, as a button that opens the customer's sheet over the one it is in. */
const props = defineProps<{ tenantId: string; externalId: string }>();
const customers = useCustomers(() => props.tenantId);
const customer = computed(() => (customers.data.value?.customers ?? []).find((c) => c.external_id === props.externalId));
</script>

<template>
  <button type="button" class="bg-muted/50 hover:bg-muted flex w-full min-w-0 flex-col rounded-xl px-3 py-2 text-start text-sm" data-testid="related-customer" @click="peek('customer', tenantId, externalId)">
    <span class="font-medium break-words" dir="auto">{{ customer?.name || externalId }}</span>
    <span class="text-muted-foreground text-xs break-words"><span class="mono">{{ externalId }}</span><template v-if="customer?.email"> · {{ maskEmail(customer.email) }}</template></span>
  </button>
</template>
