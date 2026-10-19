<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { useTenantInvoices } from "./api";

const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const invoices = useTenantInvoices(() => props.tenantId);
</script>

<template>
  <p v-if="invoices.error.value" class="error">{{ toApiError(invoices.error.value).message }}</p>
  <table>
    <thead>
      <tr>
        <th>{{ t("tenant.columns.number") }}</th>
        <th>{{ t("tenant.columns.kind") }}</th>
        <th>{{ t("tenant.columns.period") }}</th>
        <th>{{ t("tenant.columns.customer") }}</th>
        <th class="num">{{ t("tenant.columns.total") }}</th>
        <th>{{ t("tenant.columns.status") }}</th>
        <th>{{ t("tenant.columns.saga") }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="inv in invoices.data.value?.invoices ?? []" :key="inv.id">
        <td>
          <Link to="/tenants/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: inv.id ?? '' }">{{ inv.number || t("tenant.draft") }}</Link>
        </td>
        <td>{{ label(t, te, "invoiceKind", inv.kind) }}</td>
        <td>{{ inv.period_id }}</td>
        <td>{{ inv.customer_external_id }}</td>
        <td class="num">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</td>
        <td>{{ label(t, te, "invoiceStatus", inv.status) }}</td>
        <td class="mono">{{ inv.saga_state }}</td>
      </tr>
    </tbody>
  </table>
  <p v-if="!invoices.isPending.value && (invoices.data.value?.invoices ?? []).length === 0 && !invoices.error.value" class="muted">
    {{ t("tenant.noInvoices") }}
  </p>
</template>
