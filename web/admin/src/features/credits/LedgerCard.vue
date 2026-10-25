<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
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
</script>

<template>
  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.ledger.title") }}</h2>
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
        <TableRow v-for="e in ledger.data.value?.entries ?? []" :key="e.id">
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
  </Card>
</template>
