<script setup lang="ts">
import { Link, useRouter } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { enumWord, label } from "@/shared/lib/labels";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { SortSelect } from "@/shared/ui/sort-select";
import { nameAsc, nameDesc, newest, oldest, time } from "@/shared/lib/sortDefs";

import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { useTableState } from "@/shared/lib/useTableState";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Button } from "@/shared/ui/button";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { runBulk } from "@/shared/lib/bulk";
import { askConfirm } from "@/shared/lib/confirm";
import { useSetTenantStatus, useTenants } from "./api";

const { t, te } = useI18n();
const tenants = useTenants();
const router = useRouter();
const open = (id?: string) => void router.navigate({ to: "/tenants/$id", params: { id: id ?? "" } });
const list = computed(() => tenants.data.value?.tenants ?? []);
const setStatus = useSetTenantStatus();
const suspended = (x: { status?: string }) => x.status === "TENANT_STATUS_SUSPENDED";
/** Only an active tenant can be suspended and only a suspended one reinstated (an inactive one is activated from its own page, with a reason). */
const canSuspend = (x: { status?: string }) => x.status === "TENANT_STATUS_ACTIVE";
const note = ref("");
async function change(rows: typeof list.value, status: "active" | "suspended") {
  const can = rows.filter((x) => (status === "suspended" ? canSuspend(x) : suspended(x)));
  if (can.length === 0) return;
  if (status === "suspended" && !(await askConfirm({ body: t("tenants.suspendConfirm", { n: can.length }, can.length) }))) return;
  const r = await runBulk(can, (x) => setStatus.mutateAsync({ id: x.id ?? "", status }));
  for (const x of r.ok) table.tick(x, false);
  note.value = t("common.bulk.done", { ok: r.ok.length, failed: r.failed.length }) + (r.failed.length ? ` · ${t("common.bulk.notChanged")}: ${r.failed.map((f) => f.row.name).join(", ")}` : "");
}
const envOf = (v?: string) => enumWord(v, "ENVIRONMENT_");
const tierOf = (v?: string) => enumWord(v, "TENANT_TIER_");
const table = useTableState(list, {
  sorts: () => [nameAsc(t, (x) => x.name), nameDesc(t, (x) => x.name), newest(t, (x) => time(x.create_time)), oldest(t, (x) => time(x.create_time))],
  sheet: { kind: "tenant", id: (x) => x.id }, selectId: (x) => x.id, searchFields: (x) => [x.name, x.id, x.currency, x.timezone],
  url: "f_", filters: { env: (x, v) => envOf(x.environment) === v, tier: (x, v) => tierOf(x.tier) === v, currency: (x, v) => x.currency === v },
});
const distinct = (pick: (x: (typeof list.value)[number]) => string | undefined) => computed(() => [...new Set(list.value.map((x) => pick(x) ?? "").filter(Boolean))].sort());
const envs = distinct((x) => envOf(x.environment));
const tiers = distinct((x) => tierOf(x.tier));
const currencies = distinct((x) => x.currency);
</script>

<template>
  <PageHeader :title="t('tenants.title')" />
  <Skeleton v-if="tenants.isPending.value" class="mb-3 h-6 w-40" aria-busy="true" />
  <ErrorText :error="tenants.error.value" class="mb-3" />
  <Card class="p-3">
    <BulkBar :count="table.selectedRows.value.length" @clear="table.clearSelection()">
      <Button type="button" size="sm" variant="outline" data-testid="bulk-suspend" @click="change(table.selectedRows.value, 'suspended')">{{ t("tenants.suspend") }}</Button>
      <Button type="button" size="sm" variant="outline" data-testid="bulk-reinstate" @click="change(table.selectedRows.value, 'active')">{{ t("tenants.reinstate") }}</Button>
    </BulkBar>
    <p v-if="note" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ note }}</p>
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="table.filters.value.env" multiple :label="t('tenants.columns.environment')" :options="[{ value: '', label: t('tenants.allEnvironments') }, ...envs.map((e) => ({ value: e, label: label(t, te, 'environment', e) }))]" />
      <FilterSelect v-model="table.filters.value.tier" multiple :label="t('tenants.columns.tier')" :options="[{ value: '', label: t('tenants.allTiers') }, ...tiers.map((x) => ({ value: x, label: label(t, te, 'tier', x) }))]" />
      <FilterSelect v-model="table.filters.value.currency" multiple :label="t('tenants.columns.currency')" :options="[{ value: '', label: t('tenants.allCurrencies') }, ...currencies.map((c) => ({ value: c }))]" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
          <TableHead>{{ t("tenants.columns.name") }}</TableHead>
          <TableHead>{{ t("tenants.columns.environment") }}</TableHead>
          <TableHead>{{ t("tenants.columns.tier") }}</TableHead>
          <TableHead>{{ t("tenants.columns.currency") }}</TableHead>
          <TableHead>{{ t("tenants.columns.timezone") }}</TableHead>
          <TableHead>{{ t("tenants.columns.id") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("billing.webhooks.actions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="tenant in table.items.value" :key="tenant.id" class="cursor-pointer" @click="open(tenant.id)">
          <TableCell><RowCheck :label="`${t('common.bulk.selectRow')} ${tenant.name}`" :checked="table.isTicked(tenant)" @change="(on) => table.tick(tenant, on)" /></TableCell>
          <TableCell>
            <Link to="/tenants/$id" :params="{ id: tenant.id ?? '' }" class="font-medium hover:underline" @click.stop>{{ tenant.name }}</Link>
            <Badge v-if="suspended(tenant)" variant="destructive" class="ms-2" data-testid="tenant-suspended">{{ t("tenants.suspended") }}</Badge>
          </TableCell>
          <TableCell><Badge variant="secondary">{{ label(t, te, "environment", enumWord(tenant.environment, "ENVIRONMENT_")) }}</Badge></TableCell>
          <TableCell>{{ label(t, te, "tier", enumWord(tenant.tier, "TENANT_TIER_")) }}</TableCell>
          <TableCell>{{ tenant.currency }}</TableCell>
          <TableCell>{{ tenant.timezone }}</TableCell>
          <TableCell class="mono text-muted-foreground max-w-40 truncate text-xs" :title="tenant.id">{{ tenant.id }}</TableCell>
          <TableCell><RowActions :actions="[{ key: 'open', label: t('common.view'), onSelect: () => open(tenant.id) }, ...(canSuspend(tenant) ? [{ key: 'suspend', label: t('tenants.suspend'), separatorBefore: true, onSelect: () => change([tenant], 'suspended') }] : []), ...(suspended(tenant) ? [{ key: 'reinstate', label: t('tenants.reinstate'), separatorBefore: true, onSelect: () => change([tenant], 'active') }] : [])]" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <TableEmpty v-if="!tenants.isPending.value && (tenants.data.value?.tenants ?? []).length === 0 && !tenants.error.value">
      {{ t("tenants.empty") }}
    </TableEmpty>
    <TableEmpty v-else-if="list.length > 0 && table.total.value === 0">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
