<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { ArrowLeft, FileText } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { ManualPaymentsCard } from "@/features/invoices";
import { useErrorText } from "@/shared/lib/errors";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useInvoice, useOpenInvoicePdf } from "./billingApi";

/**
 * One invoice: its total and status, who it is for, its lines and the payments recorded for it by hand, and a link to the PDF (a presigned link, valid five
 * minutes). It reads /v1/invoices/{id} and /pdf; the saga timeline, the transfer controls and the credit-note actions are operator tools and are not here.
 */
const { t, te, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));
const invoice = useInvoice(tenantId, invoiceId);
const pdf = useOpenInvoicePdf(tenantId, invoiceId);
const inv = computed(() => invoice.data.value);
const day = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "");
</script>

<template>
  <section class="flex flex-col gap-6">
    <Link to="/app/t/$id/invoices" :params="{ id: tenantId }" class="text-muted-foreground hover:text-foreground inline-flex w-fit items-center gap-1 px-1 text-sm">
      <ArrowLeft class="size-4 rtl:rotate-180" />{{ t("product.billing.invoice.back") }}
    </Link>

    <p v-if="invoice.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(invoice.error.value) }}</p>
    <Skeleton v-if="invoice.isPending.value" class="h-48 rounded-3xl" />

    <template v-if="inv">
      <div class="surface flex flex-wrap items-start justify-between gap-4 p-6">
        <div class="flex flex-col gap-1">
          <p class="text-muted-foreground text-sm">{{ label(t, te, "invoiceKind", inv.kind) }}</p>
          <h2 class="text-2xl font-semibold tracking-tight" data-testid="invoice-number">{{ inv.number || t("product.billing.invoices.draft") }}</h2>
          <p class="text-4xl font-semibold tracking-tight tabular" data-testid="invoice-total">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</p>
          <div class="mt-1"><StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill></div>
        </div>
        <div class="flex flex-col items-start gap-3 sm:items-end">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
            <dt class="text-muted-foreground">{{ t("product.billing.invoices.customer") }}</dt>
            <dd class="font-medium">{{ inv.customer_external_id }}</dd>
            <dt class="text-muted-foreground">{{ t("product.billing.invoices.period") }}</dt>
            <dd>{{ inv.period_id }}</dd>
            <dt class="text-muted-foreground">{{ t("product.billing.invoice.issued") }}</dt>
            <dd>{{ day(inv.finalize_time || inv.create_time) }}</dd>
          </dl>
          <Button :disabled="!inv.number || pdf.isPending.value" @click="pdf.mutate()"><FileText />{{ t("product.billing.invoice.openPdf") }}</Button>
          <p v-if="pdf.error.value" class="text-destructive text-sm" role="alert">{{ errorText(pdf.error.value) }}</p>
        </div>
      </div>

      <section class="surface flex flex-col gap-3 p-6" :aria-label="t('product.billing.invoice.lines')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.billing.invoice.lines") }}</h2>
        <div class="-mx-2 overflow-x-auto px-2">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-muted-foreground text-xs">
                <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoice.description") }}</th>
                <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoice.service") }}</th>
                <th scope="col" class="py-2 pe-4 text-end font-medium">{{ t("product.billing.invoice.quantity") }}</th>
                <th scope="col" class="py-2 text-end font-medium">{{ t("product.billing.invoice.amount") }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="line in inv.lines ?? []" :key="line.line_no" class="border-border/70 border-t">
                <td class="py-3 pe-4">{{ line.description }}<span v-if="line.meter_code" class="text-muted-foreground mono block text-xs">{{ line.meter_code }}</span></td>
                <td class="text-muted-foreground py-3 pe-4 whitespace-nowrap">{{ day(line.service_from) }}<template v-if="line.service_from"> – {{ day(line.service_to) }}</template></td>
                <td class="py-3 pe-4 text-end tabular">{{ line.quantity }}</td>
                <td class="py-3 text-end font-medium whitespace-nowrap tabular">{{ formatMinor(line.amount_minor, inv.currency, locale) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-border/70 border-t">
                <th scope="row" colspan="3" class="py-3 pe-4 text-end font-medium">{{ t("product.billing.invoice.total") }}</th>
                <td class="py-3 text-end font-semibold whitespace-nowrap tabular">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>

      <ManualPaymentsCard v-if="(inv.payments ?? []).length > 0" :payments="inv.payments ?? []" :currency="inv.currency ?? ''" />
    </template>
  </section>
</template>
