<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import CreditNotesCard from "@/features/creditnotes/CreditNotesCard.vue";
import { useLedger, useReconcile } from "./api";

const { t, te, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const ledger = useLedger(tenantId);
const reconcile = useReconcile(tenantId);

const now = new Date();
const period = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`);
const result = computed(() => reconcile.data.value);
const kinds = computed(() => Object.entries(result.value?.discrepancy_counts ?? {}));
const when = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "short", timeStyle: "short" }).format(new Date(v)) : "");
const variant = (status?: string) => (status === "succeeded" ? "success" : status === "declined" || status === "failed" ? "destructive" : "secondary");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.payments.reconcile") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.payments.reconcileHint") }}</p>
    <form class="flex flex-wrap items-center gap-3" role="form" :aria-label="t('billing.payments.reconcile')" @submit.prevent="reconcile.mutate(period)">
      <Input v-model="period" class="w-40" :aria-label="t('billing.payments.period')" placeholder="2026-10" pattern="\d{4}-(0[1-9]|1[0-2])" required />
      <Button type="submit" :disabled="reconcile.isPending.value">{{ t("billing.payments.run") }}</Button>
    </form>
    <p v-if="reconcile.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(reconcile.error.value).message }}</p>
    <div v-if="result" role="region" :aria-label="t('billing.payments.reconcileResult')">
      <p class="text-sm">{{ t("billing.payments.summary", { paid: result.paid_invoices ?? 0, charges: result.successful_charges ?? 0 }) }}</p>
      <p v-if="kinds.length === 0" class="bg-success/10 text-success mt-2 rounded-2xl px-4 py-3 text-sm" role="status">{{ t("billing.payments.agree") }}</p>
      <ul v-else class="mt-2 flex flex-wrap gap-2">
        <li v-for="[kind, n] in kinds" :key="kind"><Badge variant="destructive">{{ label(t, te, "discrepancy", kind) }}: {{ n }}</Badge></li>
      </ul>
      <Table v-if="(result.discrepancies ?? []).length > 0">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.payments.kind") }}</TableHead>
            <TableHead>{{ t("billing.payments.invoice") }}</TableHead>
            <TableHead>{{ t("billing.payments.provider") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.payments.billed") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.payments.ledgerAmount") }}</TableHead>
            <TableHead>{{ t("billing.payments.detail") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(d, i) in result.discrepancies" :key="i">
            <TableCell>{{ label(t, te, "discrepancy", d.kind) }}</TableCell>
            <TableCell>
              <Link v-if="d.invoice_id" to="/tenants/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: d.invoice_id }" class="mono hover:underline">{{ d.number || d.invoice_id.slice(0, 8) }}</Link>
            </TableCell>
            <TableCell>{{ d.provider }} <span class="mono text-xs">{{ d.provider_ref }}</span></TableCell>
            <TableCell class="text-end tabular-nums">{{ d.billed_minor ? formatMinor(String(d.billed_minor), d.currency, locale) : "" }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ d.ledger_minor ? formatMinor(String(d.ledger_minor), d.currency, locale) : "" }}</TableCell>
            <TableCell class="text-muted-foreground">{{ d.detail }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="result.truncated" class="text-muted-foreground text-xs">{{ t("billing.payments.truncated") }}</p>
    </div>
  </Card>

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.payments.ledger") }}</h2>
    <p v-if="ledger.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(ledger.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.payments.when") }}</TableHead>
          <TableHead>{{ t("billing.payments.kind") }}</TableHead>
          <TableHead>{{ t("billing.payments.status") }}</TableHead>
          <TableHead>{{ t("billing.payments.provider") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.payments.amount") }}</TableHead>
          <TableHead>{{ t("billing.payments.reference") }}</TableHead>
          <TableHead>{{ t("billing.payments.source") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in ledger.data.value?.entries ?? []" :key="e.id">
          <TableCell class="whitespace-nowrap">{{ when(e.occurred_at) }}</TableCell>
          <TableCell>{{ label(t, te, "ledgerKind", e.kind) }}</TableCell>
          <TableCell><Badge :variant="variant(e.status)">{{ label(t, te, "ledgerStatus", e.status) }}</Badge></TableCell>
          <TableCell>{{ e.provider }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ formatMinor(String(e.amount_minor ?? 0), e.currency, locale) }}</TableCell>
          <TableCell class="mono text-xs">{{ e.provider_ref }}</TableCell>
          <TableCell>{{ label(t, te, "ledgerSource", e.source) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!ledger.isPending.value && (ledger.data.value?.entries ?? []).length === 0 && !ledger.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.payments.empty") }}
    </p>
  </Card>

  <CreditNotesCard :tenant-id="tenantId" class="mt-6" />
</template>
