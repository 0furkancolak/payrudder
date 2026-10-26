<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useInvoice } from "@/features/product/billingApi";
import CustomerRef from "./CustomerRef.vue";
import InvoiceRef from "./InvoiceRef.vue";
import RelatedSection from "./RelatedSection.vue";

/**
 * Who paid and what for, in the sheet of a payment (or, with `payer` off, of a payment link): the customer, when the record names one or its invoice does, and the invoice, each a link
 * that opens its own sheet.
 */
const props = withDefaults(defineProps<{ tenantId: string; invoiceId?: string; customer?: string; payer?: boolean }>(), { invoiceId: "", customer: "", payer: true });
const { t } = useI18n();
const invoice = useInvoice(() => props.tenantId, () => props.invoiceId ?? "");
const customer = computed(() => props.customer || (props.invoiceId ? invoice.data.value?.customer_external_id : "") || "");
</script>

<template>
  <div v-if="invoiceId || customer" class="flex flex-col gap-4" data-testid="payment-context">
    <RelatedSection v-if="customer" :title="payer ? t('related.paidBy') : t('related.customer')">
      <li><CustomerRef :tenant-id="tenantId" :external-id="customer" /></li>
    </RelatedSection>
    <RelatedSection v-if="invoiceId" :title="t('related.invoice')">
      <li><InvoiceRef :tenant-id="tenantId" :invoice-id="invoiceId" /></li>
    </RelatedSection>
  </div>
</template>
