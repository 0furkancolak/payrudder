<script setup lang="ts">
import { computed } from "vue";
import { useCustomerInvoices } from "./api";
import InvoiceRows from "./InvoiceRows.vue";
import PaymentRows from "./PaymentRows.vue";
import SubscriptionRows from "./SubscriptionRows.vue";

/** What a customer has, for their sheet: invoices, the payments made for them and subscriptions. Every row opens the sheet of that record. */
const props = defineProps<{ tenantId: string; externalId: string }>();
const invoices = useCustomerInvoices(() => props.tenantId, () => props.externalId);
const invoiceIds = computed(() => (invoices.data.value?.invoices ?? []).map((i) => i.id ?? ""));
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="customer-related">
    <InvoiceRows :tenant-id="tenantId" :customer="externalId" />
    <PaymentRows :tenant-id="tenantId" :invoice-ids="invoiceIds" />
    <SubscriptionRows :tenant-id="tenantId" :customer="externalId" />
  </div>
</template>
