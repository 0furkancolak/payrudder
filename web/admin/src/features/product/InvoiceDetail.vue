<script setup lang="ts">
import { FileText } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { lineText } from "@/features/invoices/lineText";
import { findTenant } from "@/shared/lib/environment";
import { NativeSelect } from "@/shared/ui/native-select";
import { useFormat } from "@/shared/lib/format";
import { SCENARIOS } from "./golive";
import { useApps } from "./api";
import { ManualPaymentsCard } from "@/features/invoices";
import { InvoiceRelated } from "@/features/related";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useTenantInvoice, useOpenInvoicePdf, useSimulateInvoice } from "./billingApi";

/**
 * The content of one invoice, for the sheet the invoice list opens (the sheet's header already names the invoice): its total and status, who it is for, its lines and the payments recorded for it by hand, the records related to it, and a link to the PDF (a presigned link, valid five
 * minutes). It reads /v1/invoices/{id} and /pdf; the saga timeline, the transfer controls and the credit-note actions are operator tools and are not here.
 */
const { t, te, locale } = useI18n();
const fmt = useFormat();
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const tenantId = computed(() => props.tenantId);
const invoiceId = computed(() => props.invoiceId);
const invoice = useTenantInvoice(tenantId, invoiceId);
const pdf = useOpenInvoicePdf(tenantId, invoiceId);
const inv = computed(() => invoice.data.value);
const day = (v?: string | null) => fmt.date(v, "day");

// Sandbox only: run the payment of this invoice again as one of the scenarios (the customer keeps the scenario as their payment method).
const apps = useApps();
const sandbox = computed(() => findTenant(apps.data.value?.apps, tenantId.value)?.env === "sandbox");
const simulate = useSimulateInvoice(tenantId, invoiceId);
const scenario = ref("declined");
const canSimulate = computed(() => sandbox.value && (inv.value?.status === "open" || inv.value?.status === "uncollectible"));
</script>

<template>
  <section class="flex flex-col gap-6">
    <ErrorState :error="invoice.error.value" :retrying="invoice.isFetching.value" class="mb-4" @retry="invoice.refetch()" />
    <Skeleton v-if="invoice.isPending.value" class="h-48 rounded-3xl" />

    <template v-if="inv">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="flex flex-col gap-1">
          <p class="text-muted-foreground text-sm">{{ label(t, te, "invoiceKind", inv.kind) }}</p>
          <h2 class="sr-only" data-testid="invoice-number">{{ inv.number || t("product.billing.invoices.draft") }}</h2>
          <p class="text-4xl font-semibold tracking-tight tabular-nums" data-testid="invoice-total">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</p>
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
          <ErrorText :error="pdf.error.value" />
        </div>
      </div>

      <section v-if="canSimulate" class="bg-muted/50 flex flex-col gap-3 rounded-2xl p-4" :aria-label="t('product.sandbox.simulateTitle')" data-testid="simulate">
        <div>
          <h3 class="text-lg font-semibold tracking-tight">{{ t("product.sandbox.simulateTitle") }}</h3>
          <p class="text-muted-foreground text-sm">{{ t("product.sandbox.simulateBody") }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <NativeSelect v-model="scenario" class="w-72" :aria-label="t('product.sandbox.scenariosTitle')" data-testid="simulate-scenario">
            <option v-for="s in SCENARIOS" :key="s.scenario" :value="s.scenario">{{ s.scenario }}</option>
          </NativeSelect>
          <Button :disabled="simulate.isPending.value" data-testid="simulate-run" @click="simulate.mutate(scenario)">{{ t("product.sandbox.simulate") }}</Button>
        </div>
        <p v-if="simulate.data.value" class="text-sm" role="status" data-testid="simulate-result">
          {{ simulate.data.value.retried ? t("product.sandbox.simulated", { scenario: simulate.data.value.scenario ?? scenario }) : t("product.sandbox.simulateNotRetried", { why: simulate.data.value.retry_error ?? "" }) }}
        </p>
        <ErrorText :error="simulate.error.value" />
      </section>

      <section class="flex flex-col gap-3" :aria-label="t('product.billing.invoice.lines')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.billing.invoice.lines") }}</h2>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("product.billing.invoice.description") }}</TableHead>
              <TableHead>{{ t("product.billing.invoice.service") }}</TableHead>
              <TableHead class="text-end">{{ t("product.billing.invoice.quantity") }}</TableHead>
              <TableHead class="text-end">{{ t("product.billing.invoice.amount") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="line in inv.lines ?? []" :key="line.line_no" class="hover:bg-transparent">
              <TableCell>{{ lineText(line.description, t, (v) => fmt.utcDay(v, "day", v)) }}<span v-if="line.meter_code" class="text-muted-foreground mono block text-xs">{{ line.meter_code }}</span></TableCell>
              <TableCell class="text-muted-foreground whitespace-nowrap">{{ day(line.service_from) }}<template v-if="line.service_from"> – {{ day(line.service_to) }}</template></TableCell>
              <TableCell class="text-end tabular-nums">{{ line.quantity }}</TableCell>
              <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">{{ formatMinor(line.amount_minor, inv.currency, locale) }}</TableCell>
            </TableRow>
            <TableRow class="hover:bg-transparent">
              <TableHead scope="row" colspan="3" class="text-foreground text-end text-sm font-medium">{{ t("product.billing.invoice.total") }}</TableHead>
              <TableCell class="text-end font-semibold whitespace-nowrap tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </section>

      <ManualPaymentsCard v-if="(inv.payments ?? []).length > 0" :payments="inv.payments ?? []" :currency="inv.currency ?? ''" />

      <InvoiceRelated :tenant-id="tenantId" :invoice-id="invoiceId" :customer="inv.customer_external_id" />
    </template>
  </section>
</template>
