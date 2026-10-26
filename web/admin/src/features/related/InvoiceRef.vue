<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useInvoice } from "@/features/product/billingApi";
import { formatMinor } from "@/shared/lib/money";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { recordLink } from "./link";

/** An invoice by its id: number, status and total, as a link that opens the invoice's sheet. */
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const { t, locale } = useI18n();
const invoice = useInvoice(() => props.tenantId, () => props.invoiceId);
const inv = computed(() => invoice.data.value);
const link = computed(() => recordLink("invoice", props.invoiceId));
</script>

<template>
  <Link :to="link.to" :params="{ id: tenantId }" :search="link.search" class="bg-muted/50 hover:bg-muted flex items-center justify-between gap-3 rounded-xl px-3 py-2 text-sm" data-testid="related-invoice">
    <span class="mono font-medium">{{ inv ? inv.number || t("product.billing.invoices.draft") : invoiceId.slice(0, 8) }}</span>
    <span v-if="inv" class="flex items-center gap-2">
      <StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill>
      <span class="tabular font-medium">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</span>
    </span>
  </Link>
</template>
