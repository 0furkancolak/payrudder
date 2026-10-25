<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import type { BadgeVariants } from "@/shared/ui/badge";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import type { InvoiceTransfer } from "./payment";

/** What the bank transfer of an invoice is waiting for (reference, bank details, what has arrived) and, while pending, how to give up on it. */
const props = defineProps<{ transfer: InvoiceTransfer }>();
const emit = defineEmits<{ confirm: []; reject: [outcome: "rejected" | "expired"] }>();
const { t, te } = useI18n();
const fmt = useFormat();

const state = computed(() => props.transfer.state ?? "none");
const tone = computed<NonNullable<BadgeVariants["variant"]>>(() => (state.value === "paid" ? "success" : state.value === "pending" ? "outline" : "destructive"));
const money = (v: number | undefined) => fmt.amount(v, props.transfer.currency);
const when = (v?: string) => fmt.date(v, "dateTime", "—");
const bank = computed(() => props.transfer.instructions);
</script>

<template>
  <Card class="mb-6 gap-3" role="region" :aria-label="t('invoice.transfer.title')" data-testid="transfer-card">
    <div class="flex flex-wrap items-center gap-3">
      <h2 class="text-lg font-semibold">{{ t("invoice.transfer.title") }}</h2>
      <Badge :variant="tone" data-testid="transfer-state">{{ label(t, te, "transferState", state) }}</Badge>
      <div v-if="state === 'pending'" class="ms-auto flex flex-wrap gap-2">
        <Button type="button" size="sm" @click="emit('confirm')">{{ t("invoice.payment.confirmTransfer") }}</Button>
        <Button type="button" size="sm" variant="outline" @click="emit('reject', 'rejected')">{{ t("invoice.transfer.reject") }}</Button>
        <Button type="button" size="sm" variant="outline" @click="emit('reject', 'expired')">{{ t("invoice.transfer.expire") }}</Button>
      </div>
    </div>
    <p v-if="state === 'expired' || state === 'rejected'" class="text-muted-foreground text-sm">
      {{ t(state === "expired" ? "invoice.transfer.expiredNote" : "invoice.transfer.rejectedNote") }}
      <template v-if="transfer.closed_reason"> {{ transfer.closed_reason }}</template>
    </p>

    <div class="grid gap-4 sm:grid-cols-2">
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted-foreground">{{ t("invoice.transfer.reference") }}</dt>
        <dd class="mono font-medium" data-testid="transfer-reference">{{ transfer.reference || bank?.reference }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.bank") }}</dt>
        <dd>{{ bank?.bank_name }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.holder") }}</dt>
        <dd>{{ bank?.account_holder }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.iban") }}</dt>
        <dd class="mono" data-testid="transfer-iban">{{ bank?.iban }}</dd>
        <template v-if="bank?.bic">
          <dt class="text-muted-foreground">{{ t("invoice.transfer.bic") }}</dt>
          <dd class="mono">{{ bank.bic }}</dd>
        </template>
      </dl>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted-foreground">{{ t("invoice.transfer.total") }}</dt>
        <dd class="tabular-nums">{{ money(transfer.total_minor) }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.received") }}</dt>
        <dd class="tabular-nums" data-testid="transfer-received">{{ money(transfer.received_minor) }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.outstanding") }}</dt>
        <dd class="font-medium tabular-nums" data-testid="transfer-outstanding">{{ money(transfer.outstanding_minor) }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.pendingSince") }}</dt>
        <dd>{{ when(transfer.pending_since) }}</dd>
        <dt class="text-muted-foreground">{{ t("invoice.transfer.expiresAt") }}</dt>
        <dd>{{ when(transfer.expires_at) }}</dd>
      </dl>
    </div>

    <template v-if="(transfer.receipts ?? []).length > 0">
      <h3 class="text-base font-semibold">{{ t("invoice.transfer.receipts") }}</h3>
      <Table :aria-label="t('invoice.transfer.receipts')">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("invoice.transfer.receiptColumns.receivedAt") }}</TableHead>
            <TableHead class="text-end">{{ t("invoice.transfer.receiptColumns.amount") }}</TableHead>
            <TableHead>{{ t("invoice.transfer.receiptColumns.bankReference") }}</TableHead>
            <TableHead>{{ t("invoice.transfer.receiptColumns.note") }}</TableHead>
            <TableHead>{{ t("invoice.transfer.receiptColumns.recordedBy") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in transfer.receipts" :key="r.id">
            <TableCell class="whitespace-nowrap">{{ when(r.received_at) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ money(r.amount_minor) }}</TableCell>
            <TableCell class="mono text-xs">{{ r.bank_reference }}</TableCell>
            <TableCell>{{ r.note }}</TableCell>
            <TableCell class="text-muted-foreground text-xs">{{ r.recorded_by }} · {{ when(r.recorded_at) }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </template>
  </Card>
</template>
