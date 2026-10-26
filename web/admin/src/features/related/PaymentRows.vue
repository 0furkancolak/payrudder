<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { usePayments } from "@/features/product/billingApi";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The payment attempts (charges and refunds) of some invoices, newest first; each opens the payment's sheet. */
const props = defineProps<{ tenantId: string; invoiceIds: string[] }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const payments = usePayments(() => props.tenantId);
const rows = computed(() => (payments.data.value?.payments ?? []).filter((p) => props.invoiceIds.includes(p.invoice_id ?? "")));
</script>

<template>
  <RelatedSection :title="t('related.payments')" :empty="rows.length === 0">
    <RelatedRow v-for="p in rows" :id="p.id ?? ''" :key="p.id" :tenant-id="tenantId" kind="payment">
      <span class="flex flex-col">
        <span class="capitalize">{{ t(`product.billing.payments.kinds.${p.kind}`) }} · {{ p.provider }}</span>
        <span class="text-muted-foreground text-xs">{{ fmt.date(p.occurred_at, "dateTime") }}</span>
      </span>
      <span class="flex items-center gap-2">
        <StatusPill :tone="toneOf(p.status)">{{ t(`product.status.${p.status}`) }}</StatusPill>
        <span class="tabular font-medium">{{ formatMinor(p.status === "refunded" ? `-${p.amount_minor}` : p.amount_minor, p.currency, locale) }}</span>
      </span>
    </RelatedRow>
  </RelatedSection>
</template>
