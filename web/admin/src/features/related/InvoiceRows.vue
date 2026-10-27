<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useCustomerInvoices } from "./api";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The invoices of one customer, newest first (the first `limit` of them when given); each opens the invoice's sheet. `bare` leaves the title to the reader of a screen. */
const props = defineProps<{ tenantId: string; customer: string; limit?: number; bare?: boolean; emptyText?: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const invoices = useCustomerInvoices(() => props.tenantId, () => props.customer);
const all = computed(() => invoices.data.value?.invoices ?? []);
const rows = computed(() => (props.limit ? all.value.slice(0, props.limit) : all.value));
</script>

<template>
  <RelatedSection :title="t('related.invoices')" :empty="!invoices.isPending.value && rows.length === 0" :hide-title="bare" :empty-text="emptyText">
    <template v-if="$slots.action && all.length > rows.length" #action><slot name="action" /></template>
    <template v-if="invoices.isPending.value">
      <li v-for="n in 2" :key="n"><Skeleton class="h-12 w-full rounded-xl" /></li>
    </template>
    <RelatedRow v-for="i in rows" v-else :id="i.id ?? ''" :key="i.id" :tenant-id="tenantId" kind="invoice">
      <span class="flex min-w-0 flex-col">
        <span class="mono truncate font-medium">{{ i.number || t("product.billing.invoices.draft") }}</span>
        <span class="text-muted-foreground text-xs">{{ fmt.date(i.finalize_time || i.create_time, "day") }}</span>
      </span>
      <span class="flex shrink-0 items-center gap-2">
        <StatusPill :tone="toneOf(i.status)">{{ t(`product.invoiceStatus.${i.status}`) }}</StatusPill>
        <span class="tabular-nums font-medium">{{ formatMinor(i.total_minor, i.currency, locale) }}</span>
      </span>
    </RelatedRow>
  </RelatedSection>
</template>
