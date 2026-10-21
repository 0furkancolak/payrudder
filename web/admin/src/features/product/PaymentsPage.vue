<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { usePayments } from "./billingApi";

/**
 * The environment's payments, newest first: charges and refunds, each as the one outcome it settled on (pending, paid, failed, refunded). Reads /v1/payments.
 * The operator's ledger (provider references, decline details) and the reconciliation are operator tools and are not here.
 */
const { t, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const payments = usePayments(tenantId);
const rows = computed(() => payments.data.value?.payments ?? []);
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeStyle: "short" }).format(new Date(v)) : "");
</script>

<template>
  <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.billing.payments.title')">
    <div class="flex flex-wrap items-baseline justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.billing.payments.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.billing.payments.intro") }}</p>
      </div>
      <span v-if="payments.data.value?.truncated" class="text-muted-foreground text-xs">{{ t("product.payments.truncated") }}</span>
    </div>

    <p v-if="payments.error.value" class="bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm" role="alert">{{ errorText(payments.error.value) }}</p>
    <Skeleton v-if="payments.isPending.value" class="h-40 rounded-2xl" />
    <p v-else-if="rows.length === 0 && !payments.error.value" class="text-muted-foreground py-10 text-center text-sm">{{ t("product.billing.payments.empty") }}</p>
    <div v-else class="-mx-2 overflow-x-auto px-2">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-muted-foreground text-xs">
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.when") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.payments.kind") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.status") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.payments.provider") }}</th>
            <th scope="col" class="py-2 pe-4 text-end font-medium">{{ t("product.payments.amount") }}</th>
            <th scope="col" class="py-2 text-start font-medium"><span class="sr-only">{{ t("product.billing.payments.invoice") }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in rows" :key="p.id" class="border-border/70 border-t" data-testid="payment-row">
            <td class="py-3 pe-4 whitespace-nowrap">{{ when(p.occurred_at) }}</td>
            <td class="py-3 pe-4">{{ t(`product.billing.payments.kinds.${p.kind}`) }}</td>
            <td class="py-3 pe-4"><StatusPill :tone="toneOf(p.status)">{{ t(`product.status.${p.status}`) }}</StatusPill></td>
            <td class="text-muted-foreground py-3 pe-4 capitalize">{{ p.provider }}</td>
            <td class="py-3 pe-4 text-end font-medium whitespace-nowrap tabular">{{ formatMinor(p.status === "refunded" ? `-${p.amount_minor}` : p.amount_minor, p.currency, locale) }}</td>
            <td class="py-3 whitespace-nowrap">
              <Link v-if="p.invoice_id" to="/app/t/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: p.invoice_id }" class="text-primary text-xs font-medium hover:underline">
                {{ t("product.billing.payments.viewInvoice") }}
              </Link>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
