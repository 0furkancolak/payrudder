<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useCustomerInvoices } from "./api";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The invoices of one customer, newest first; each opens the invoice's sheet. */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const invoices = useCustomerInvoices(() => props.tenantId, () => props.customer);
</script>

<template>
  <RelatedSection :title="t('related.invoices')" :empty="(invoices.data.value?.invoices ?? []).length === 0">
    <RelatedRow v-for="i in invoices.data.value?.invoices ?? []" :id="i.id ?? ''" :key="i.id" :tenant-id="tenantId" kind="invoice">
      <span class="flex flex-col">
        <span class="mono font-medium">{{ i.number || t("product.billing.invoices.draft") }}</span>
        <span class="text-muted-foreground text-xs">{{ fmt.date(i.finalize_time || i.create_time, "day") }}</span>
      </span>
      <span class="flex items-center gap-2">
        <StatusPill :tone="toneOf(i.status)">{{ t(`product.invoiceStatus.${i.status}`) }}</StatusPill>
        <span class="tabular font-medium">{{ formatMinor(i.total_minor, i.currency, locale) }}</span>
      </span>
    </RelatedRow>
  </RelatedSection>
</template>
