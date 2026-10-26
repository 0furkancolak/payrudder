<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { NativeSelect } from "@/shared/ui/native-select";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useTenantInvoices } from "./api";

const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const invoices = useTenantInvoices(() => props.tenantId);
const list = computed(() => invoices.data.value?.invoices ?? []);
const STATUSES = ["draft", "open", "paid", "uncollectible", "void"] as const;
const table = useTableState(list, {
  searchFields: (inv) => [inv.number, inv.period_id, inv.customer_external_id, inv.total_minor],
  filters: { status: (inv, v) => inv.status === v, kind: (inv, v) => inv.kind === v },
});
const setFilter = (key: string) => computed({ get: () => table.filters.value[key] ?? "", set: (v: string) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const statusFilter = setFilter("status");
const kindFilter = setFilter("kind");
</script>

<template>
  <ErrorText :error="invoices.error.value" class="px-3" />
  <DataToolbar v-if="list.length" v-model:search="table.search.value" :active="table.active.value" class="pt-2" @clear="table.clear()">
    <NativeSelect v-model="statusFilter" class="w-40" :aria-label="t('tenant.columns.status')">
      <option value="">{{ t("tenant.columns.status") }}</option>
      <option v-for="s in STATUSES" :key="s" :value="s">{{ label(t, te, "invoiceStatus", s) }}</option>
    </NativeSelect>
    <NativeSelect v-model="kindFilter" class="w-40" :aria-label="t('tenant.columns.kind')">
      <option value="">{{ t("tenant.columns.kind") }}</option>
      <option v-for="k in ['REGULAR', 'CORRECTION', 'CREDIT_NOTE']" :key="k" :value="k">{{ label(t, te, "invoiceKind", k) }}</option>
    </NativeSelect>
  </DataToolbar>
  <p v-if="list.length && !table.total.value" class="text-muted-foreground px-3 py-4 text-sm">{{ t("common.noMatches") }}</p>
  <Table v-else>
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
      <TableRow v-for="inv in table.items.value" :key="inv.id">
        <TableCell>
          <Link to="/tenants/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: inv.id ?? '' }" class="font-medium hover:underline">{{ inv.number || t("tenant.draft") }}</Link>
        </TableCell>
        <TableCell>{{ label(t, te, "invoiceKind", inv.kind) }}</TableCell>
        <TableCell>{{ inv.period_id }}</TableCell>
        <TableCell>{{ inv.customer_external_id }}</TableCell>
        <TableCell class="text-end font-medium tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
        <TableCell><Badge :variant="statusVariant(inv.status)">{{ label(t, te, "invoiceStatus", inv.status) }}</Badge></TableCell>
        <TableCell><Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ label(t, te, "sagaState", inv.saga_state) }}</Badge></TableCell>
      </TableRow>
    </TableBody>
  </Table>
  <DataPagination v-if="list.length" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
  <p v-if="!invoices.isPending.value && list.length === 0 && !invoices.error.value" class="text-muted-foreground p-4 text-center text-sm">
    {{ t("tenant.noInvoices") }}
  </p>
</template>
