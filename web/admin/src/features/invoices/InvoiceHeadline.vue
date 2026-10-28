<script setup lang="ts">
import { FileText } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";

/**
 * The top of an invoice's sheet, the same for the people who run an app and for the operator: what kind it is, the total, its status, who it is for, the
 * period and the day it was issued, and the button for the PDF. The `pills` slot adds more status words beside the invoice's own (the operator's saga state).
 */
defineProps<{
  inv: { kind?: string; number?: string; total_minor?: string; currency?: string; status?: string; customer_external_id?: string; period_id?: string; finalize_time?: string | null; create_time?: string | null };
  pdfPending: boolean;
  pdfError?: unknown;
}>();
defineEmits<{ openPdf: [] }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
</script>

<template>
  <div class="flex flex-wrap items-start justify-between gap-4">
    <div class="flex flex-col gap-1">
      <p class="text-muted-foreground text-sm">{{ label(t, te, "invoiceKind", inv.kind) }}</p>
      <p class="sr-only" data-testid="invoice-number">{{ inv.number || t("product.billing.invoices.draft") }}</p>
      <p class="text-4xl font-semibold tracking-tight tabular-nums" data-testid="invoice-total">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</p>
      <div class="mt-1 flex flex-wrap gap-2">
        <StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill>
        <slot name="pills" />
      </div>
    </div>
    <div class="flex flex-col items-start gap-3 sm:items-end">
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted-foreground">{{ t("product.billing.invoices.customer") }}</dt>
        <dd class="font-medium">{{ inv.customer_external_id }}</dd>
        <dt class="text-muted-foreground">{{ t("product.billing.invoices.period") }}</dt>
        <dd>{{ inv.period_id }}</dd>
        <dt class="text-muted-foreground">{{ t("product.billing.invoice.issued") }}</dt>
        <dd>{{ fmt.date(inv.finalize_time || inv.create_time, "day") }}</dd>
      </dl>
      <div class="flex flex-wrap items-center gap-2 sm:justify-end">
        <slot name="actions" />
        <Button type="button" :disabled="!inv.number || pdfPending" @click="$emit('openPdf')"><FileText />{{ t("product.billing.invoice.openPdf") }}</Button>
      </div>
      <ErrorText :error="pdfError" />
    </div>
  </div>
</template>
