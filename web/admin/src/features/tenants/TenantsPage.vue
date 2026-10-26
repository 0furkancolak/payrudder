<script setup lang="ts">
import { Link, useRouter } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { enumWord, label } from "@/shared/lib/labels";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useTenants } from "./api";

const { t, te } = useI18n();
const tenants = useTenants();
const router = useRouter();
const open = (id?: string) => void router.navigate({ to: "/tenants/$id", params: { id: id ?? "" } });
const list = computed(() => tenants.data.value?.tenants ?? []);
const envOf = (v?: string) => enumWord(v, "ENVIRONMENT_");
const tierOf = (v?: string) => enumWord(v, "TENANT_TIER_");
const table = useTableState(list, {
  searchFields: (x) => [x.name, x.id, x.currency, x.timezone],
  filters: { env: (x, v) => envOf(x.environment) === v, tier: (x, v) => tierOf(x.tier) === v, currency: (x, v) => x.currency === v },
});
const distinct = (pick: (x: (typeof list.value)[number]) => string | undefined) => computed(() => [...new Set(list.value.map((x) => pick(x) ?? "").filter(Boolean))].sort());
const envs = distinct((x) => envOf(x.environment));
const tiers = distinct((x) => tierOf(x.tier));
const currencies = distinct((x) => x.currency);
</script>

<template>
  <PageHeader :title="t('tenants.title')" />
  <p v-if="tenants.isPending.value" class="text-muted-foreground">{{ t("common.loading") }}</p>
  <ErrorText :error="tenants.error.value" class="mb-3" />
  <Card class="p-3">
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <FilterSelect v-model="table.filters.value.env" :label="t('tenants.columns.environment')" :options="[{ value: '', label: t('tenants.allEnvironments') }, ...envs.map((e) => ({ value: e, label: label(t, te, 'environment', e) }))]" />
      <FilterSelect v-model="table.filters.value.tier" :label="t('tenants.columns.tier')" :options="[{ value: '', label: t('tenants.allTiers') }, ...tiers.map((x) => ({ value: x, label: label(t, te, 'tier', x) }))]" />
      <FilterSelect v-model="table.filters.value.currency" :label="t('tenants.columns.currency')" :options="[{ value: '', label: t('tenants.allCurrencies') }, ...currencies.map((c) => ({ value: c }))]" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
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
          <TableCell>
            <Link to="/tenants/$id" :params="{ id: tenant.id ?? '' }" class="font-medium hover:underline" @click.stop>{{ tenant.name }}</Link>
          </TableCell>
          <TableCell><Badge variant="secondary">{{ label(t, te, "environment", enumWord(tenant.environment, "ENVIRONMENT_")) }}</Badge></TableCell>
          <TableCell>{{ label(t, te, "tier", enumWord(tenant.tier, "TENANT_TIER_")) }}</TableCell>
          <TableCell>{{ tenant.currency }}</TableCell>
          <TableCell>{{ tenant.timezone }}</TableCell>
          <TableCell class="mono text-muted-foreground max-w-40 truncate text-xs" :title="tenant.id">{{ tenant.id }}</TableCell>
          <TableCell><RowActions :actions="[{ key: 'open', label: t('common.view'), onSelect: () => open(tenant.id) }]" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!tenants.isPending.value && (tenants.data.value?.tenants ?? []).length === 0 && !tenants.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("tenants.empty") }}
    </p>
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
