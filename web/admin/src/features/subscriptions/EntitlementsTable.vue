<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEntitlements } from "./api";

/** How much of each limit of the package a customer has used this period. */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t } = useI18n();
const entitlements = useEntitlements(
  () => props.tenantId,
  () => props.customer,
);

/** How much of the limit is used, for the bar: a display value only, the exact decimals are what is written. */
const percent = (used?: string, limit?: string | null) => (limit && Number(limit) > 0 ? Math.min(100, Math.round((Number(used ?? 0) / Number(limit)) * 100)) : 0);
</script>

<template>
  <div role="region" :aria-label="t('billing.subscriptions.usage')" class="grid gap-2">
    <h3 class="text-base font-semibold">{{ t("billing.subscriptions.usage") }}</h3>
    <ErrorText :error="entitlements.error.value" />
    <Table v-if="(entitlements.data.value?.items ?? []).length > 0">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.subscriptions.meter") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.subscriptions.used") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.subscriptions.limit") }}</TableHead>
          <TableHead class="w-40" />
          <TableHead>{{ t("billing.subscriptions.allowed") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in entitlements.data.value?.items ?? []" :key="e.meter_code">
          <TableCell class="mono">{{ e.meter_code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ e.used }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ e.unlimited ? t("billing.subscriptions.unlimited") : e.limit }}</TableCell>
          <TableCell>
            <div v-if="!e.unlimited" class="bg-secondary h-2 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="percent(e.used, e.limit)" aria-valuemin="0" aria-valuemax="100">
              <div class="h-full rounded-full" :class="e.allowed ? 'bg-primary' : 'bg-destructive'" :style="{ width: `${percent(e.used, e.limit)}%` }" />
            </div>
          </TableCell>
          <TableCell><Badge :variant="e.allowed ? 'success' : 'destructive'">{{ e.allowed ? t("billing.subscriptions.withinLimit") : t("billing.subscriptions.overLimit") }}</Badge></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-else-if="!entitlements.isPending.value && !entitlements.error.value" class="text-muted-foreground text-sm">{{ t("billing.subscriptions.noLimits") }}</p>
  </div>
</template>
