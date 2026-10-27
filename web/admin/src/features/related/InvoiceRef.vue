<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useInvoice } from "@/features/product/billingApi";
import { formatMinor } from "@/shared/lib/money";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { peek } from "./peek";

/** An invoice by its id: number, status and total, as a link that opens the invoice's sheet. */
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const { t, locale } = useI18n();
const invoice = useInvoice(() => props.tenantId, () => props.invoiceId);
const inv = computed(() => invoice.data.value);
</script>

<template>
  <button type="button" class="bg-muted/50 hover:bg-muted flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 text-start text-sm" data-testid="related-invoice" @click="peek('invoice', tenantId, invoiceId)">
    <span class="mono font-medium">{{ inv ? inv.number || t("product.billing.invoices.draft") : invoiceId.slice(0, 8) }}</span>
    <span v-if="inv" class="flex items-center gap-2">
      <StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill>
      <span class="tabular-nums font-medium">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</span>
    </span>
  </button>
</template>
