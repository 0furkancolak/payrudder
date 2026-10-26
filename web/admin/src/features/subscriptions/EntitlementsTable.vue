<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { modeWord, windowWord } from "@/features/meters/quota";
import { useFormat } from "@/shared/lib/format";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEntitlements } from "./api";

/** How much of each limit a customer has used in its current window (the billing month, or an hour, a day, a week), and what reaching it means. */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t } = useI18n();
const fmt = useFormat();
const entitlements = useEntitlements(
  () => props.tenantId,
  () => props.customer,
);

/** How much of the limit is used, for the bar: a display value only, the exact decimals are what is written. */
const percent = (used?: string, limit?: string | null) => (limit && Number(limit) > 0 ? Math.min(100, Math.round((Number(used ?? 0) / Number(limit)) * 100)) : 0);

type Item = NonNullable<NonNullable<typeof entitlements.data.value>["items"]>[number];
/** Over the limit whatever the mode; a gateway that does not say so yet is read from `allowed`. */
const over = (e: Item) => !e.unlimited && (e.over_limit ?? !e.allowed);
const status = (e: Item): { variant: "success" | "destructive" | "secondary"; text: string } => {
  if (!over(e)) return { variant: "success", text: t("billing.subscriptions.withinLimit") };
  return e.allowed ? { variant: "secondary", text: t("billing.quotas.overButAllowed") } : { variant: "destructive", text: t("billing.subscriptions.overLimit") };
};
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
          <TableHead class="w-32" />
          <TableHead>{{ t("billing.quotas.windowColumn") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.allowed") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in entitlements.data.value?.items ?? []" :key="e.meter_code">
          <TableCell class="mono">
            {{ e.meter_code }}
            <Badge v-if="e.overridden" variant="outline" class="ms-1">{{ t("billing.quotas.overridden") }}</Badge>
          </TableCell>
          <TableCell class="text-end tabular-nums">{{ e.used }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ e.unlimited ? t("billing.subscriptions.unlimited") : e.limit }}</TableCell>
          <TableCell>
            <div v-if="!e.unlimited" class="bg-secondary h-2 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="percent(e.used, e.limit)" aria-valuemin="0" aria-valuemax="100">
              <div class="h-full rounded-full" :class="over(e) ? (e.allowed ? 'bg-warning' : 'bg-destructive') : 'bg-primary'" :style="{ width: `${percent(e.used, e.limit)}%` }" />
            </div>
          </TableCell>
          <TableCell class="text-sm whitespace-nowrap">
            <template v-if="!e.unlimited">
              {{ t(`billing.quotas.windows.${windowWord(e.window)}`) }}
              <span v-if="e.resets_at" class="text-muted-foreground block text-xs">{{ t("billing.quotas.resets", { at: fmt.date(e.resets_at, "dateTime") }) }}</span>
            </template>
          </TableCell>
          <TableCell>
            <Badge :variant="status(e).variant">{{ status(e).text }}</Badge>
            <span v-if="!e.unlimited && e.mode && modeWord(e.mode) !== 'hard'" class="text-muted-foreground block text-xs">{{ t(`billing.quotas.modes.${modeWord(e.mode)}`) }}</span>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-else-if="!entitlements.isPending.value && !entitlements.error.value" class="text-muted-foreground text-sm">{{ t("billing.subscriptions.noLimits") }}</p>
  </div>
</template>
