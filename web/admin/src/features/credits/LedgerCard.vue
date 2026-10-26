<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreditLedger } from "./api";
import { formatCredits, kindVariant } from "./lib";

/** Everything that happened to one customer's credits, newest first. */
const props = defineProps<{ tenantId: string; customer: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const ledger = useCreditLedger(
  () => props.tenantId,
  () => props.customer,
);
type Entry = NonNullable<NonNullable<typeof ledger.data.value>["entries"]>[number];
const table = useTableState<Entry>(() => ledger.data.value?.entries ?? [], {
  searchFields: (e) => [e.reason, e.kind ? t(`billing.credits.kind.${e.kind}`) : "", e.source ? t(`billing.credits.source.${e.source}`) : ""],
  filters: { kind: (e, v) => e.kind === v },
});
const kindFilter = computed({ get: () => table.filters.value.kind ?? "", set: (v) => (table.filters.value.kind = String(v)) });
</script>

<template>
  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.ledger.title") }}</h2>
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" class="pt-3" @clear="table.clear()">
      <NativeSelect v-model="kindFilter" class="w-44" :aria-label="t('billing.credits.ledger.kind')">
        <option value="">{{ t("billing.credits.ledger.kind") }}</option>
        <option v-for="k in ['grant', 'consume', 'expire']" :key="k" :value="k">{{ t(`billing.credits.kind.${k}`) }}</option>
      </NativeSelect>
    </DataToolbar>
    <ErrorText :error="ledger.error.value" class="px-3" />
    <p v-if="ledger.data.value && !(ledger.data.value.entries ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.ledger.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.ledger.time") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.kind") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.source") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.ledger.amount") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.reason") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in table.items.value" :key="e.id">
          <TableCell>{{ fmt.date(e.create_time, "dateTime") }}</TableCell>
          <TableCell>
            <Badge :variant="kindVariant(e.kind)">{{ t(`billing.credits.kind.${e.kind}`) }}</Badge>
          </TableCell>
          <TableCell>
            <Badge v-if="e.source === 'purchase'" variant="success">{{ t("billing.credits.source.purchase") }}</Badge>
            <span v-else-if="e.source" class="text-muted-foreground text-sm">{{ t(`billing.credits.source.${e.source}`) }}</span>
          </TableCell>
          <TableCell class="text-end tabular-nums">{{ formatCredits(e.amount, locale) }}</TableCell>
          <TableCell class="text-muted-foreground">{{ e.reason }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="table.total.value === 0 && table.active.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>
</template>
