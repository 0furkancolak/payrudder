<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { findTenant } from "@/shared/lib/environment";
import { NativeSelect } from "@/shared/ui/native-select";
import { SCENARIOS } from "./golive";
import { useApps } from "./api";
import { InvoiceHeadline, InvoiceLines, ManualPaymentsCard } from "@/features/invoices";
import { InvoiceRelated } from "@/features/related";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { Button } from "@/shared/ui/button";
import { Skeleton } from "@/shared/ui/skeleton";
import { useTenantInvoice, useOpenInvoicePdf, useSimulateInvoice } from "./billingApi";

/**
 * The content of one invoice, for the sheet the invoice list opens (the sheet's header already names the invoice): its total and status, who it is for, its lines and the payments recorded for it by hand, the records related to it, and a link to the PDF (a presigned link, valid five
 * minutes). It reads /v1/invoices/{id} and /pdf; the saga timeline, the transfer controls and the credit-note actions are operator tools and are not here.
 */
const { t } = useI18n();
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const tenantId = computed(() => props.tenantId);
const invoiceId = computed(() => props.invoiceId);
const invoice = useTenantInvoice(tenantId, invoiceId);
const pdf = useOpenInvoicePdf(tenantId, invoiceId);
const inv = computed(() => invoice.data.value);

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
      <InvoiceHeadline :inv="inv" :pdf-pending="pdf.isPending.value" :pdf-error="pdf.error.value" @open-pdf="pdf.mutate()" />

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

      <InvoiceLines :inv="inv" />

      <ManualPaymentsCard v-if="(inv.payments ?? []).length > 0" :payments="inv.payments ?? []" :currency="inv.currency ?? ''" />

      <InvoiceRelated :tenant-id="tenantId" :invoice-id="invoiceId" :customer="inv.customer_external_id" />
    </template>
  </section>
</template>
