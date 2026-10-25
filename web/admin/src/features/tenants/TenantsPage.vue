<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { enumWord, label } from "@/shared/lib/labels";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useTenants } from "./api";

const { t, te } = useI18n();
const tenants = useTenants();
</script>

<template>
  <PageHeader :title="t('tenants.title')" />
  <p v-if="tenants.isPending.value" class="text-muted-foreground">{{ t("common.loading") }}</p>
  <ErrorText :error="tenants.error.value" class="mb-3" />
  <Card class="p-3">
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("tenants.columns.name") }}</TableHead>
          <TableHead>{{ t("tenants.columns.environment") }}</TableHead>
          <TableHead>{{ t("tenants.columns.tier") }}</TableHead>
          <TableHead>{{ t("tenants.columns.currency") }}</TableHead>
          <TableHead>{{ t("tenants.columns.timezone") }}</TableHead>
          <TableHead>{{ t("tenants.columns.id") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="tenant in tenants.data.value?.tenants ?? []" :key="tenant.id">
          <TableCell>
            <Link to="/tenants/$id" :params="{ id: tenant.id ?? '' }" class="font-medium hover:underline">{{ tenant.name }}</Link>
          </TableCell>
          <TableCell><Badge variant="secondary">{{ label(t, te, "environment", enumWord(tenant.environment, "ENVIRONMENT_")) }}</Badge></TableCell>
          <TableCell>{{ label(t, te, "tier", enumWord(tenant.tier, "TENANT_TIER_")) }}</TableCell>
          <TableCell>{{ tenant.currency }}</TableCell>
          <TableCell>{{ tenant.timezone }}</TableCell>
          <TableCell class="mono text-muted-foreground max-w-40 truncate text-xs" :title="tenant.id">{{ tenant.id }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!tenants.isPending.value && (tenants.data.value?.tenants ?? []).length === 0 && !tenants.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("tenants.empty") }}
    </p>
  </Card>
</template>
