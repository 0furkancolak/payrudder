<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { usePayments } from "@/features/product/billingApi";
import { useFormat } from "@/shared/lib/format";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import PaymentDetail from "./PaymentDetail.vue";
import { signedAmount } from "./payment";

/**
 * A payment's sheet opened over the sheet that mentions it (an invoice's, a customer's), so the reader is not taken to the payments page: it stands on its own,
 * with no arrows, and emits `close`. There is no call for one payment, so it is found among the newest payments the console loads (the list's own); an older one says so.
 */
const props = defineProps<{ tenantId: string; paymentId: string }>();
const emit = defineEmits<{ close: [] }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const payments = usePayments(() => props.tenantId);
const payment = computed(() => (payments.data.value?.payments ?? []).find((p) => p.id === props.paymentId));
</script>

<template>
  <DetailSheet :index="0" :total="1" :title="payment ? signedAmount(payment, locale) : paymentId" :description="payment ? fmt.date(payment.occurred_at, 'dateTime') : undefined" @update:index="emit('close')">
    <ErrorState :error="payments.error.value" :retrying="payments.isFetching.value" @retry="payments.refetch()" />
    <Skeleton v-if="payments.isPending.value" class="h-32 rounded-3xl" />
    <PaymentDetail v-else-if="payment" :tenant-id="tenantId" :payment="payment" />
    <p v-else-if="!payments.error.value" class="text-muted-foreground text-sm" data-testid="payment-peek-missing">{{ t("related.paymentNotLoaded") }}</p>
  </DetailSheet>
</template>
