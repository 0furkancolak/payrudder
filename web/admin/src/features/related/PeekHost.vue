<script setup lang="ts">
import { computed } from "vue";
import OperatorInvoiceSheet from "@/features/invoices/InvoiceSheetById.vue";
import CustomerSheetById from "@/features/customers/CustomerSheetById.vue";
import SubscriptionSheetById from "@/features/subscriptions/SubscriptionSheetById.vue";
import PersonInvoiceSheet from "@/features/product/InvoiceSheetById.vue";
import { names } from "@/shared/lib/sheets";
import { tenantBase } from "@/shared/lib/session";
import { parsePeek, unpeek } from "./peek";

/** The sheets of records opened over another sheet (see peek.ts), one for each such name in the stack; mounted once, in the frame of the pages. */
const peeks = computed(() => names.value.flatMap((n) => parsePeek(n) ?? []));
const invoiceSheet = computed(() => (tenantBase() === "/app/t" ? PersonInvoiceSheet : OperatorInvoiceSheet));
</script>

<template>
  <template v-for="p in peeks" :key="`${p.kind}:${p.tenantId}:${p.id}`">
    <component :is="invoiceSheet" v-if="p.kind === 'invoice'" :tenant-id="p.tenantId" :invoice-id="p.id" @close="unpeek('invoice', p.tenantId, p.id)" />
    <SubscriptionSheetById v-else-if="p.kind === 'subscription'" :tenant-id="p.tenantId" :subscription-id="p.id" @close="unpeek('subscription', p.tenantId, p.id)" />
    <CustomerSheetById v-else-if="p.kind === 'customer'" :tenant-id="p.tenantId" :external-id="p.id" @close="unpeek('customer', p.tenantId, p.id)" />
  </template>
</template>
