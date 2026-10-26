<script setup lang="ts">
import { useI18n } from "vue-i18n";
import CreditNoteRows from "./CreditNoteRows.vue";
import CustomerRef from "./CustomerRef.vue";
import PaymentRows from "./PaymentRows.vue";
import RelatedSection from "./RelatedSection.vue";
import SubscriptionRows from "./SubscriptionRows.vue";

/**
 * What hangs off an invoice, for its sheet: the customer it is for, the payment attempts made for it, its credit notes (`creditNotes: false` when the sheet
 * already lists them) and the customer's subscriptions. Every row opens the sheet of that record.
 */
withDefaults(defineProps<{ tenantId: string; invoiceId: string; customer?: string; creditNotes?: boolean }>(), { customer: "", creditNotes: true });
const { t } = useI18n();
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="invoice-related">
    <RelatedSection v-if="customer" :title="t('related.customer')"><li><CustomerRef :tenant-id="tenantId" :external-id="customer" /></li></RelatedSection>
    <PaymentRows :tenant-id="tenantId" :invoice-ids="[invoiceId]" />
    <CreditNoteRows v-if="creditNotes" :tenant-id="tenantId" :invoice-id="invoiceId" />
    <SubscriptionRows v-if="customer" :tenant-id="tenantId" :customer="customer" />
  </div>
</template>
