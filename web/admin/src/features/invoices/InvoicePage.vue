<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { useInvoice, useInvoiceSaga, useOpenPdf } from "./api";

const { t, te, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));

const invoice = useInvoice(tenantId, invoiceId);
const saga = useInvoiceSaga(tenantId, invoiceId);
const pdf = useOpenPdf(tenantId, invoiceId);
const inv = computed(() => invoice.data.value);
</script>

<template>
  <p>
    <Link to="/tenants/$id" :params="{ id: tenantId }">{{ t("invoice.back") }}</Link>
  </p>
  <h1>{{ inv?.number || t("invoice.fallbackTitle") }}</h1>
  <p v-if="invoice.error.value" class="error">{{ toApiError(invoice.error.value).message }}</p>
  <p v-if="inv" class="muted">
    {{
      t("invoice.summary", {
        kind: label(t, te, "invoiceKind", inv.kind),
        period: inv.period_id,
        customer: inv.customer_external_id,
        total: formatMinor(inv.total_minor, inv.currency, locale),
        status: label(t, te, "invoiceStatus", inv.status),
        saga: inv.saga_state,
      })
    }}
  </p>
  <div class="row">
    <button type="button" :disabled="!inv?.number || pdf.isPending.value" @click="pdf.mutate()">{{ t("invoice.openPdf") }}</button>
    <span v-if="pdf.error.value" class="error">{{ toApiError(pdf.error.value).message }}</span>
  </div>

  <h2>{{ t("invoice.lines") }}</h2>
  <table>
    <thead>
      <tr>
        <th>{{ t("invoice.lineColumns.no") }}</th>
        <th>{{ t("invoice.lineColumns.kind") }}</th>
        <th>{{ t("invoice.lineColumns.description") }}</th>
        <th>{{ t("invoice.lineColumns.meter") }}</th>
        <th class="num">{{ t("invoice.lineColumns.quantity") }}</th>
        <th class="num">{{ t("invoice.lineColumns.amount") }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="line in inv?.lines ?? []" :key="line.line_no">
        <td>{{ line.line_no }}</td>
        <td>{{ line.kind }}</td>
        <td>{{ line.description }}</td>
        <td>{{ line.meter_code }}</td>
        <td class="num">{{ line.quantity }}</td>
        <td class="num">{{ formatMinor(line.amount_minor, inv?.currency, locale) }}</td>
      </tr>
    </tbody>
  </table>

  <h2>{{ t("invoice.saga") }}</h2>
  <p v-if="saga.error.value" class="error">{{ toApiError(saga.error.value).message }}</p>
  <table :aria-label="t('invoice.saga')">
    <thead>
      <tr>
        <th class="num">{{ t("invoice.sagaColumns.seq") }}</th>
        <th>{{ t("invoice.sagaColumns.from") }}</th>
        <th>{{ t("invoice.sagaColumns.to") }}</th>
        <th>{{ t("invoice.sagaColumns.cause") }}</th>
        <th>{{ t("invoice.sagaColumns.at") }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="e in saga.data.value?.events ?? []" :key="e.seq" data-testid="saga-event">
        <td class="num">{{ e.seq }}</td>
        <td class="mono">{{ e.from_state }}</td>
        <td class="mono">{{ e.to_state }}</td>
        <td class="mono">{{ e.cause }}</td>
        <td class="mono">{{ e.at }}</td>
      </tr>
    </tbody>
  </table>
</template>
