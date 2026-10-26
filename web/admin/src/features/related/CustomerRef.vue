<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useCustomers } from "@/features/customers/api";
import { maskEmail } from "@/shared/lib/money";
import { recordLink } from "./link";

/** A customer by their external id: name, id and e-mail when the customer list has them, as a link that opens the customer's sheet. */
const props = defineProps<{ tenantId: string; externalId: string }>();
const customers = useCustomers(() => props.tenantId);
const customer = computed(() => (customers.data.value?.customers ?? []).find((c) => c.external_id === props.externalId));
const link = computed(() => recordLink("customer", props.externalId));
</script>

<template>
  <Link :to="link.to" :params="{ id: tenantId }" :search="link.search" class="bg-muted/50 hover:bg-muted flex min-w-0 flex-col rounded-xl px-3 py-2 text-sm" data-testid="related-customer">
    <span class="font-medium break-words" dir="auto">{{ customer?.name || externalId }}</span>
    <span class="text-muted-foreground text-xs break-words"><span class="mono">{{ externalId }}</span><template v-if="customer?.email"> · {{ maskEmail(customer.email) }}</template></span>
  </Link>
</template>
