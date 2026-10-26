<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Plus } from "@lucide/vue";
import { useFormat } from "@/shared/lib/format";
import { useTenant } from "@/features/tenants/api";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Input } from "@/shared/ui/input";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { useTableState } from "@/shared/lib/useTableState";
import EInvoiceAccountSheet from "./EInvoiceAccountSheet.vue";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEInvoiceAccountActions, useEInvoiceAccounts, useSaveEInvoiceSettings, type EInvoiceOn, type LegalInfo } from "./api";
import type { Environment } from "@/shared/lib/environment";

const { t } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);

// --- when an invoice becomes an e-document, and the seller's company data
const save = useSaveEInvoiceSettings(tenantId);
const eInvoiceOn = ref<EInvoiceOn>("off");
const legal = ref<LegalInfo>({});
watch(
  () => tenant.data.value,
  (v) => {
    eInvoiceOn.value = (v?.e_invoice_on as EInvoiceOn | undefined) || "off";
    legal.value = { ...(v?.legal ?? {}) };
  },
  { immediate: true },
);
function saveSettings() {
  const trimmed = Object.fromEntries(Object.entries(legal.value).map(([k, v]) => [k, (v ?? "").trim()]));
  save.mutate({ legal: trimmed, eInvoiceOn: eInvoiceOn.value });
}

// --- the integrator account
const accounts = useEInvoiceAccounts(tenantId);
const actions = useEInvoiceAccountActions(tenantId);
const accountOpen = ref(false);
const list = computed(() => accounts.data.value ?? []);
const table = useTableState(list, {
  sheet: { kind: "einvoice", id: (a) => a.environment }, searchFields: (a) => [a.provider, a.environment],
  filters: { environment: (a, v) => a.environment === v },
});
const environmentOptions = computed(() => [
  { value: "", label: t("einvoice.account.allEnvironments") },
  { value: "live", label: "live" },
  { value: "sandbox", label: "sandbox" },
]);
const environmentFilter = computed({ get: () => table.filters.value.environment ?? "", set: (v: string) => (table.filters.value = { ...table.filters.value, environment: v }) });
function removeAccount(env: Environment, p: string) {
  if (window.confirm(t("einvoice.settings.confirmRemove", { provider: p, environment: env }))) actions.remove.mutate(env);
}
const when = (v?: string) => fmt.date(v, "day", "—");
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("einvoice.account.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("einvoice.account.hint") }}</p>
    </div>
    <ErrorText :error="accounts.error.value" class="px-3" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" class="pt-3" @clear="table.clear()">
      <FilterSelect v-model="environmentFilter" :label="t('einvoice.account.environment')" :options="environmentOptions" />
      <template #actions>
        <Button type="button" @click="accountOpen = true"><Plus class="size-4" aria-hidden="true" />{{ t("einvoice.account.add") }}</Button>
      </template>
    </DataToolbar>
    <p v-if="list.length && !table.total.value" class="text-muted-foreground px-3 py-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <Table v-else-if="list.length">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("einvoice.account.provider") }}</TableHead>
          <TableHead>{{ t("einvoice.account.environment") }}</TableHead>
          <TableHead>{{ t("einvoice.account.mode") }}</TableHead>
          <TableHead>{{ t("einvoice.account.credentials") }}</TableHead>
          <TableHead>{{ t("einvoice.account.updated") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="a in table.items.value" :key="a.environment" data-testid="einvoice-account">
          <TableCell class="font-medium">{{ a.provider }}</TableCell>
          <TableCell>{{ a.environment }}</TableCell>
          <TableCell>{{ a.mode ? t(`einvoice.account.modes.${a.mode}`) : "—" }}</TableCell>
          <TableCell><Badge :variant="a.has_credentials ? 'success' : 'secondary'">{{ a.has_credentials ? t("einvoice.account.stored") : t("einvoice.account.none") }}</Badge></TableCell>
          <TableCell>{{ when(a.updated_at) }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="[{ key: 'remove', label: t('billing.common.remove'), destructive: true, onSelect: () => removeAccount((a.environment ?? 'live') as 'sandbox' | 'live', a.provider ?? '') }]" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.length" v-model:size="table.size.value" :page="table.current.value" :pages="table.pages.value" :total="table.total.value" @update:page="table.page.value = $event" />
    <p v-if="!accounts.isPending.value && (accounts.data.value ?? []).length === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("einvoice.account.empty") }}</p>
    <ErrorText :error="actions.remove.error.value" class="px-3" />
  </Card>

  <Card>
    <h2 class="text-lg font-semibold">{{ t("einvoice.settings.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("einvoice.settings.hint") }}</p>
    <ErrorText :error="tenant.error.value" />
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" role="form" :aria-label="t('einvoice.settings.formLabel')" @submit.prevent="saveSettings">
      <NativeSelect v-model="eInvoiceOn" :aria-label="t('einvoice.settings.on')">
        <option value="off">{{ t("einvoice.settings.onOff") }}</option>
        <option value="finalized">{{ t("einvoice.settings.onFinalized") }}</option>
        <option value="paid">{{ t("einvoice.settings.onPaid") }}</option>
      </NativeSelect>
      <Input v-model="legal.legal_name" maxlength="160" :aria-label="t('einvoice.settings.legalName')" :placeholder="t('einvoice.settings.legalName')" />
      <Input v-model="legal.tax_id" maxlength="40" :aria-label="t('einvoice.settings.taxId')" :placeholder="t('einvoice.settings.taxId')" />
      <Input v-model="legal.tax_office" maxlength="120" :aria-label="t('einvoice.settings.taxOffice')" :placeholder="t('einvoice.settings.taxOffice')" />
      <Input v-model="legal.mersis_no" maxlength="40" :aria-label="t('einvoice.settings.mersis')" :placeholder="t('einvoice.settings.mersis')" />
      <Input v-model="legal.trade_registry_no" maxlength="40" :aria-label="t('einvoice.settings.tradeRegistry')" :placeholder="t('einvoice.settings.tradeRegistry')" />
      <div class="sm:col-span-2 lg:col-span-3"><Button type="submit" :disabled="save.isPending.value">{{ t("einvoice.settings.save") }}</Button></div>
    </form>
    <ErrorText :error="save.error.value" />
    <p v-if="save.isSuccess.value" class="text-success text-sm" role="status">{{ t("einvoice.settings.saved") }}</p>
  </Card>

  <EInvoiceAccountSheet v-model:open="accountOpen" :tenant-id="tenantId" />
</template>
