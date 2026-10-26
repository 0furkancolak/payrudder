<script setup lang="ts">
import { useI18n } from "vue-i18n";
import CustomerRef from "./CustomerRef.vue";
import InvoiceRows from "./InvoiceRows.vue";
import RelatedSection from "./RelatedSection.vue";

/** What a subscription belongs to, for its sheet: the customer and that customer's invoices. Every row opens the sheet of that record. */
defineProps<{ tenantId: string; customer: string }>();
const { t } = useI18n();
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="subscription-related">
    <RelatedSection :title="t('related.customer')"><li><CustomerRef :tenant-id="tenantId" :external-id="customer" /></li></RelatedSection>
    <InvoiceRows :tenant-id="tenantId" :customer="customer" />
  </div>
</template>
