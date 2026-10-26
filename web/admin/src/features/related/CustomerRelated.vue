<script setup lang="ts">
import { ArrowRight } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useCustomerInvoices } from "./api";
import InvoiceRows from "./InvoiceRows.vue";
import PaymentRows from "./PaymentRows.vue";
import SubscriptionRows from "./SubscriptionRows.vue";

/**
 * What a customer has, for their sheet: subscriptions, invoices and the payments made for them. Every row opens the sheet of that record. With `limit`
 * each group shows its newest rows only and offers a "view all" that emits `more` with the group's name, for a sheet with a tab per group.
 */
const props = defineProps<{ tenantId: string; externalId: string; limit?: number }>();
const emit = defineEmits<{ more: ["subscriptions" | "invoices" | "payments"] }>();
const { t } = useI18n();
const invoices = useCustomerInvoices(() => props.tenantId, () => props.externalId);
const invoiceIds = computed(() => (invoices.data.value?.invoices ?? []).map((i) => i.id ?? ""));
</script>

<template>
  <div class="flex flex-col gap-6" data-testid="customer-related">
    <SubscriptionRows :tenant-id="tenantId" :customer="externalId" :limit="limit" :empty-text="t('billing.customers.sheet.noSubscriptions')">
      <template v-if="limit" #action>
        <button type="button" class="text-brand-strong inline-flex items-center gap-1 text-xs font-medium hover:underline" @click="emit('more', 'subscriptions')">{{ t("billing.customers.sheet.viewAll") }}<ArrowRight class="size-3.5 rtl:rotate-180" aria-hidden="true" /></button>
      </template>
    </SubscriptionRows>
    <InvoiceRows :tenant-id="tenantId" :customer="externalId" :limit="limit" :empty-text="t('billing.customers.sheet.noInvoices')">
      <template v-if="limit" #action>
        <button type="button" class="text-brand-strong inline-flex items-center gap-1 text-xs font-medium hover:underline" @click="emit('more', 'invoices')">{{ t("billing.customers.sheet.viewAll") }}<ArrowRight class="size-3.5 rtl:rotate-180" aria-hidden="true" /></button>
      </template>
    </InvoiceRows>
    <PaymentRows :tenant-id="tenantId" :invoice-ids="invoiceIds" :limit="limit" :empty-text="t('billing.customers.sheet.noPayments')">
      <template v-if="limit" #action>
        <button type="button" class="text-brand-strong inline-flex items-center gap-1 text-xs font-medium hover:underline" @click="emit('more', 'payments')">{{ t("billing.customers.sheet.viewAll") }}<ArrowRight class="size-3.5 rtl:rotate-180" aria-hidden="true" /></button>
      </template>
    </PaymentRows>
  </div>
</template>
