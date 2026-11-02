<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { lineText } from "@/features/invoices/lineText";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useUpcomingInvoice } from "./api";

/**
 * What the customer's next invoice would be if it were drafted now: the usage and late usage so far, the fixed fee, tax and the credit balance, worked out by
 * the same code as the real invoice (GET /v1/customers/{id}/upcoming-invoice). It is an estimate and says so; nothing here is stored.
 */
const props = defineProps<{ tenantId: string; externalId: string }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const upcoming = useUpcomingInvoice(() => props.tenantId, () => props.externalId);
</script>

<template>
  <section class="border-border/70 flex min-w-0 flex-col gap-3 rounded-2xl border p-4" :aria-label="t('billing.customers.upcoming.title')" data-testid="upcoming-invoice">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="m-0 text-sm font-semibold">{{ t("billing.customers.upcoming.title") }}</h3>
      <StatusPill tone="warning">{{ t("billing.customers.upcoming.estimate") }}</StatusPill>
    </div>
    <Skeleton v-if="upcoming.isPending.value" class="h-16 w-full" />
    <ErrorText v-else-if="upcoming.error.value" :error="upcoming.error.value" />
    <template v-else-if="upcoming.data.value">
      <p v-if="!upcoming.data.value.has_invoice" class="text-muted-foreground m-0 text-sm" data-testid="upcoming-none">{{ t("billing.customers.upcoming.none") }}</p>
      <template v-else>
        <p class="text-muted-foreground m-0 text-xs" dir="ltr">{{ t("billing.customers.upcoming.period", { period: upcoming.data.value.period_id }) }}</p>
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>{{ t("product.billing.invoice.description") }}</TableHead>
              <TableHead class="text-end">{{ t("product.billing.invoice.quantity") }}</TableHead>
              <TableHead class="text-end">{{ t("product.billing.invoice.amount") }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="line in upcoming.data.value.lines" :key="line.line_no" class="hover:bg-transparent" data-testid="upcoming-line">
              <TableCell>{{ lineText(line.description, t, (v) => fmt.utcDay(v, "day", v)) }}<span v-if="line.meter_code" class="text-muted-foreground mono block text-xs">{{ line.meter_code }}</span></TableCell>
              <TableCell class="text-end tabular-nums">{{ line.quantity }}</TableCell>
              <TableCell class="text-end font-medium whitespace-nowrap tabular-nums">{{ formatMinor(line.amount_minor, upcoming.data.value.currency, locale) }}</TableCell>
            </TableRow>
            <TableRow class="hover:bg-transparent">
              <TableHead scope="row" colspan="2" class="text-foreground text-end text-sm font-medium">{{ t("product.billing.invoice.total") }}</TableHead>
              <TableCell class="text-end font-semibold whitespace-nowrap tabular-nums" data-testid="upcoming-total">{{ formatMinor(upcoming.data.value.total_minor, upcoming.data.value.currency, locale) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p class="text-muted-foreground m-0 text-xs">{{ t("billing.customers.upcoming.note", { when: fmt.date(upcoming.data.value.as_of, "dateTime") }) }}</p>
      </template>
    </template>
  </section>
</template>
