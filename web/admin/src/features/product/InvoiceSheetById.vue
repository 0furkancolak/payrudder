<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { useTenantInvoice } from "./billingApi";
import InvoiceDetail from "./InvoiceDetail.vue";

/**
 * The sheet of one invoice that a link named but the list does not hold (the API sends the list a page at a time): it stands on its own, with no arrows
 * to walk, and emits `close` when the reader dismisses it.
 */
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();
const invoice = useTenantInvoice(() => props.tenantId, () => props.invoiceId);
</script>

<template>
  <DetailSheet :index="0" :total="1" :title="invoice.data.value?.number || t('product.billing.invoices.draft')" :description="invoice.data.value?.customer_external_id" wide @update:index="emit('close')">
    <InvoiceDetail :key="invoiceId" :tenant-id="tenantId" :invoice-id="invoiceId" />
  </DetailSheet>
</template>
