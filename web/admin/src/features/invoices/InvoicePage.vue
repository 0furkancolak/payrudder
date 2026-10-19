<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { ArrowLeft, FileText } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useInvoice, useInvoiceSaga, useOpenPdf } from "./api";

const { t, te, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));

const invoice = useInvoice(tenantId, invoiceId);
const saga = useInvoiceSaga(tenantId, invoiceId);
const pdf = useOpenPdf(tenantId, invoiceId);
const inv = computed(() => invoice.data.value);
</script>

<template>
  <Link to="/tenants/$id" :params="{ id: tenantId }" class="text-muted-foreground hover:text-foreground mb-3 inline-flex items-center gap-1 px-1 text-sm">
    <ArrowLeft class="rtl-flip size-4" />{{ t("invoice.back") }}
  </Link>
  <PageHeader :title="inv?.number || t('invoice.fallbackTitle')">
    <span v-if="pdf.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(pdf.error.value).message }}</span>
    <Button type="button" :disabled="!inv?.number || pdf.isPending.value" @click="pdf.mutate()"><FileText />{{ t("invoice.openPdf") }}</Button>
  </PageHeader>
  <p v-if="invoice.error.value" class="text-destructive mb-3" role="alert">{{ toApiError(invoice.error.value).message }}</p>

  <div v-if="inv" class="mb-6 grid gap-4 sm:grid-cols-2">
    <Card class="gap-2">
      <span class="text-muted-foreground text-sm">{{ t("tenant.columns.total") }}</span>
      <span class="text-4xl font-semibold tracking-tight tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</span>
      <div class="mt-1 flex flex-wrap gap-2">
        <Badge :variant="statusVariant(inv.status)">{{ label(t, te, "invoiceStatus", inv.status) }}</Badge>
        <Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ inv.saga_state }}</Badge>
      </div>
    </Card>
    <Card class="bg-lime text-lime-foreground gap-1 text-sm">
      <span class="font-medium">{{ label(t, te, "invoiceKind", inv.kind) }}</span>
      <span>{{ inv.period_id }}</span>
      <span>{{ inv.customer_external_id }}</span>
    </Card>
  </div>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("invoice.lines") }}</h2>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("invoice.lineColumns.no") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.kind") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.description") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.meter") }}</TableHead>
          <TableHead class="text-end">{{ t("invoice.lineColumns.quantity") }}</TableHead>
          <TableHead class="text-end">{{ t("invoice.lineColumns.amount") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="line in inv?.lines ?? []" :key="line.line_no">
          <TableCell>{{ line.line_no }}</TableCell>
          <TableCell>{{ line.kind }}</TableCell>
          <TableCell>{{ line.description }}</TableCell>
          <TableCell>{{ line.meter_code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ line.quantity }}</TableCell>
          <TableCell class="text-end font-medium tabular-nums">{{ formatMinor(line.amount_minor, inv?.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("invoice.saga") }}</h2>
    <p v-if="saga.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(saga.error.value).message }}</p>
    <Table :aria-label="t('invoice.saga')">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead class="text-end">{{ t("invoice.sagaColumns.seq") }}</TableHead>
          <TableHead>{{ t("invoice.sagaColumns.from") }}</TableHead>
          <TableHead>{{ t("invoice.sagaColumns.to") }}</TableHead>
          <TableHead>{{ t("invoice.sagaColumns.cause") }}</TableHead>
          <TableHead>{{ t("invoice.sagaColumns.at") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in saga.data.value?.events ?? []" :key="e.seq" data-testid="saga-event">
          <TableCell class="text-end tabular-nums">{{ e.seq }}</TableCell>
          <TableCell class="mono text-xs">{{ e.from_state }}</TableCell>
          <TableCell><Badge :variant="sagaVariant(e.to_state)" class="mono">{{ e.to_state }}</Badge></TableCell>
          <TableCell class="mono text-xs">{{ e.cause }}</TableCell>
          <TableCell class="mono text-muted-foreground text-xs">{{ e.at }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
