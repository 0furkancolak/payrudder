<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Card } from "@/shared/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import type { InvoicePayment } from "./payment";

/** The payments an operator recorded by hand against the invoice (cash, a transfer, other), oldest first. */
defineProps<{ payments: InvoicePayment[]; currency: string }>();
const { t, te, locale } = useI18n();
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium", timeStyle: "short" }).format(new Date(v)) : "—");
</script>

<template>
  <Card class="mb-6 p-3" role="region" :aria-label="t('invoice.payment.listTitle')">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("invoice.payment.listTitle") }}</h2>
    <Table :aria-label="t('invoice.payment.listTitle')">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("invoice.payment.columns.method") }}</TableHead>
          <TableHead class="text-end">{{ t("invoice.payment.columns.amount") }}</TableHead>
          <TableHead>{{ t("invoice.payment.columns.receivedAt") }}</TableHead>
          <TableHead>{{ t("invoice.payment.columns.reference") }}</TableHead>
          <TableHead>{{ t("invoice.payment.columns.note") }}</TableHead>
          <TableHead>{{ t("invoice.payment.columns.recordedBy") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in payments" :key="p.id" data-testid="manual-payment">
          <TableCell>{{ label(t, te, "paymentMethod", p.method) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(p.amount_minor, p.currency || currency, locale) }}</TableCell>
          <TableCell class="whitespace-nowrap">{{ when(p.received_at) }}</TableCell>
          <TableCell class="mono text-xs">{{ p.reference }}</TableCell>
          <TableCell>{{ p.note }}</TableCell>
          <TableCell class="text-muted-foreground text-xs">{{ p.recorded_by }} · {{ when(p.recorded_at) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
