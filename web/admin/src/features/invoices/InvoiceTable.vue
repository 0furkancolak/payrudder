<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useTenantInvoices } from "./api";

const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const invoices = useTenantInvoices(() => props.tenantId);
</script>

<template>
  <p v-if="invoices.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(invoices.error.value).message }}</p>
  <Table>
    <TableHeader>
      <TableRow class="hover:bg-transparent">
        <TableHead>{{ t("tenant.columns.number") }}</TableHead>
        <TableHead>{{ t("tenant.columns.kind") }}</TableHead>
        <TableHead>{{ t("tenant.columns.period") }}</TableHead>
        <TableHead>{{ t("tenant.columns.customer") }}</TableHead>
        <TableHead class="text-end">{{ t("tenant.columns.total") }}</TableHead>
        <TableHead>{{ t("tenant.columns.status") }}</TableHead>
        <TableHead>{{ t("tenant.columns.saga") }}</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="inv in invoices.data.value?.invoices ?? []" :key="inv.id">
        <TableCell>
          <Link to="/tenants/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: inv.id ?? '' }" class="font-medium hover:underline">{{ inv.number || t("tenant.draft") }}</Link>
        </TableCell>
        <TableCell>{{ label(t, te, "invoiceKind", inv.kind) }}</TableCell>
        <TableCell>{{ inv.period_id }}</TableCell>
        <TableCell>{{ inv.customer_external_id }}</TableCell>
        <TableCell class="text-end font-medium tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
        <TableCell><Badge :variant="statusVariant(inv.status)">{{ label(t, te, "invoiceStatus", inv.status) }}</Badge></TableCell>
        <TableCell><Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ inv.saga_state }}</Badge></TableCell>
      </TableRow>
    </TableBody>
  </Table>
  <p v-if="!invoices.isPending.value && (invoices.data.value?.invoices ?? []).length === 0 && !invoices.error.value" class="text-muted-foreground p-4 text-center text-sm">
    {{ t("tenant.noInvoices") }}
  </p>
</template>
