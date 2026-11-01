<script setup lang="ts">
import { useI18n } from "vue-i18n";
import type { Payment } from "@/features/product/billingApi";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { signedAmount } from "./payment";
import PaymentContext from "./PaymentContext.vue";

/** What a payment's sheet shows (the payments list's own, and the one opened over another sheet): its kind, outcome, provider and amount, then who paid and for which invoice. */
defineProps<{ tenantId: string; payment: Payment }>();
const { t, locale } = useI18n();
</script>

<template>
  <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm" data-testid="payment-detail">
    <dt class="text-muted-foreground">{{ t("product.billing.payments.kind") }}</dt>
    <dd>{{ t(`product.billing.payments.kinds.${payment.kind}`) }}</dd>
    <dt class="text-muted-foreground">{{ t("product.payments.status") }}</dt>
    <dd><StatusPill :tone="toneOf(payment.status)">{{ t(`product.status.${payment.status}`) }}</StatusPill></dd>
    <dt class="text-muted-foreground">{{ t("product.payments.provider") }}</dt>
    <dd class="capitalize">{{ payment.provider }}</dd>
    <dt class="text-muted-foreground">{{ t("product.payments.amount") }}</dt>
    <dd class="tabular-nums font-medium">{{ signedAmount(payment, locale) }}</dd>
  </dl>
  <PaymentContext v-if="payment.invoice_id" :key="payment.id" :tenant-id="tenantId" :invoice-id="payment.invoice_id" class="mt-6" />
</template>
