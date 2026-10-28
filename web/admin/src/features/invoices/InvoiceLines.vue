<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { lineText } from "./lineText";

/** The lines of an invoice and its total, as one flat table; the same for the people who run an app and for the operator. */
defineProps<{
  inv: {
    currency?: string;
    total_minor?: string;
    lines?: { line_no?: number; description?: string; meter_code?: string; service_from?: string | null; service_to?: string | null; quantity?: string | number; amount_minor?: string }[];
  };
}>();
const { t, locale } = useI18n();
const fmt = useFormat();
const day = (v?: string | null) => fmt.date(v, "day");
</script>

<template>
  <section class="flex flex-col gap-3" :aria-label="t('product.billing.invoice.lines')">
    <h2 class="text-lg font-semibold tracking-tight">{{ t("product.billing.invoice.lines") }}</h2>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("product.billing.invoice.description") }}</TableHead>
          <TableHead>{{ t("product.billing.invoice.service") }}</TableHead>
          <TableHead class="text-end">{{ t("product.billing.invoice.quantity") }}</TableHead>
          <TableHead class="text-end">{{ t("product.billing.invoice.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="line in inv.lines ?? []" :key="line.line_no" class="hover:bg-transparent">
          <TableCell>{{ lineText(line.description, t, (v) => fmt.utcDay(v, "day", v)) }}<span v-if="line.meter_code" class="text-muted-foreground mono block text-xs">{{ line.meter_code }}</span></TableCell>
          <TableCell class="text-muted-foreground whitespace-nowrap">{{ day(line.service_from) }}<template v-if="line.service_from"> – {{ day(line.service_to) }}</template></TableCell>
          <TableCell class="text-end tabular-nums">{{ line.quantity }}</TableCell>
          <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">{{ formatMinor(line.amount_minor, inv.currency, locale) }}</TableCell>
        </TableRow>
        <TableRow class="hover:bg-transparent">
          <TableHead scope="row" colspan="3" class="text-foreground text-end text-sm font-medium">{{ t("product.billing.invoice.total") }}</TableHead>
          <TableCell class="text-end font-semibold whitespace-nowrap tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </section>
</template>
