<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { usePayments } from "@/features/product/billingApi";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The payment attempts (charges and refunds) of some invoices, newest first (the first `limit` when given); each opens the payment's sheet. */
const props = defineProps<{ tenantId: string; invoiceIds: string[]; limit?: number; bare?: boolean; emptyText?: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const payments = usePayments(() => props.tenantId);
const all = computed(() => (payments.data.value?.payments ?? []).filter((p) => props.invoiceIds.includes(p.invoice_id ?? "")));
const rows = computed(() => (props.limit ? all.value.slice(0, props.limit) : all.value));
</script>

<template>
  <RelatedSection :title="t('related.payments')" :empty="!payments.isPending.value && rows.length === 0" :hide-title="bare" :empty-text="emptyText">
    <template v-if="$slots.action && all.length > rows.length" #action><slot name="action" /></template>
    <template v-if="payments.isPending.value">
      <li v-for="n in 2" :key="n"><Skeleton class="h-12 w-full rounded-xl" /></li>
    </template>
    <RelatedRow v-for="p in rows" v-else :id="p.id ?? ''" :key="p.id" :tenant-id="tenantId" kind="payment">
      <span class="flex min-w-0 flex-col">
        <span class="truncate capitalize">{{ t(`product.billing.payments.kinds.${p.kind}`) }} · {{ p.provider }}</span>
        <span class="text-muted-foreground text-xs">{{ fmt.date(p.occurred_at, "dateTime") }}</span>
      </span>
      <span class="flex shrink-0 items-center gap-2">
        <StatusPill :tone="toneOf(p.status)">{{ t(`product.status.${p.status}`) }}</StatusPill>
        <span class="tabular font-medium">{{ formatMinor(p.status === "refunded" ? `-${p.amount_minor}` : p.amount_minor, p.currency, locale) }}</span>
      </span>
    </RelatedRow>
  </RelatedSection>
</template>
