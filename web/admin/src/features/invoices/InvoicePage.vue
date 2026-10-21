<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { ArrowLeft, Banknote, FileText } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { sagaVariant, statusVariant } from "@/shared/lib/badges";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import EInvoiceStatus from "@/features/einvoice/EInvoiceStatus.vue";
import CreditNotesCard from "@/features/creditnotes/CreditNotesCard.vue";
import { Input } from "@/shared/ui/input";
import { useInvoice, useInvoiceLinks, useInvoiceSaga, useOpenPdf } from "./api";
import ManualPaymentsCard from "./ManualPaymentsCard.vue";
import { canRecordPayment, isTransferCharge, outstandingAfter, wantsTransferState } from "./payment";
import { useInvoiceTransfer } from "./paymentApi";
import RecordPaymentDialog from "./RecordPaymentDialog.vue";
import RejectTransferDialog from "./RejectTransferDialog.vue";
import TransferCard from "./TransferCard.vue";

const { t, te, locale } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));

const invoice = useInvoice(tenantId, invoiceId);
const saga = useInvoiceSaga(tenantId, invoiceId);
const pdf = useOpenPdf(tenantId, invoiceId);
const links = useInvoiceLinks(tenantId, invoiceId);
const linkDays = ref("90");
const linkError = ref("");
function makeLink() {
  linkError.value = "";
  const days = Number(linkDays.value);
  if (!Number.isInteger(days) || days < 1 || days > 365) {
    linkError.value = t("invoice.share.badDays");
    return;
  }
  links.create.mutate(days);
}
const shareUrl = computed(() => {
  const u = (links.create.data.value as { url?: string } | undefined)?.url ?? "";
  return u.startsWith("/") ? window.location.origin + u : u;
});
async function copy() {
  try {
    await navigator.clipboard.writeText(shareUrl.value);
  } catch {
    /* the field is selectable; copying by hand works */
  }
}
const inv = computed(() => invoice.data.value);

// --- money: a bank transfer that is awaited, and payments recorded by hand
const transferQuery = useInvoiceTransfer(tenantId, invoiceId, () => wantsTransferState(inv.value?.status, inv.value?.saga_state));
/** Null while loading, when the invoice is not payable, and when the transfer service did not answer: the page works without it. */
const transfer = computed(() => transferQuery.data.value ?? null);
const transferCharge = computed(() => isTransferCharge(transfer.value, inv.value?.status));
const canRecord = computed(() => canRecordPayment(inv.value?.status));
const outstanding = computed(() => (transferCharge.value ? String(transfer.value?.outstanding_minor ?? 0) : outstandingAfter(inv.value?.total_minor, inv.value?.payments)));
const recordOpen = ref(false);
const rejectOpen = ref(false);
const rejectOutcome = ref<"rejected" | "expired">("rejected");
function askReject(outcome: "rejected" | "expired") {
  rejectOutcome.value = outcome;
  rejectOpen.value = true;
}
const day = (v?: string) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "");
</script>

<template>
  <Link to="/tenants/$id" :params="{ id: tenantId }" class="text-muted-foreground hover:text-foreground mb-3 inline-flex items-center gap-1 px-1 text-sm">
    <ArrowLeft class="rtl-flip size-4" />{{ t("invoice.back") }}
  </Link>
  <PageHeader :title="inv?.number || t('invoice.fallbackTitle')">
    <span v-if="pdf.error.value" class="text-destructive text-sm" role="alert">{{ errorText(pdf.error.value) }}</span>
    <Button v-if="canRecord" type="button" variant="outline" @click="recordOpen = true"><Banknote />{{ t("invoice.payment.record") }}</Button>
    <Button type="button" :disabled="!inv?.number || pdf.isPending.value" @click="pdf.mutate()"><FileText />{{ t("invoice.openPdf") }}</Button>
  </PageHeader>
  <p v-if="invoice.error.value" class="text-destructive mb-3" role="alert">{{ errorText(invoice.error.value) }}</p>

  <div v-if="inv" class="mb-6 grid gap-4 sm:grid-cols-2">
    <Card class="gap-2">
      <span class="text-muted-foreground text-sm">{{ t("tenant.columns.total") }}</span>
      <span class="text-4xl font-semibold tracking-tight tabular-nums">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</span>
      <div class="mt-1 flex flex-wrap gap-2">
        <Badge :variant="statusVariant(inv.status)">{{ label(t, te, "invoiceStatus", inv.status) }}</Badge>
        <Badge :variant="sagaVariant(inv.saga_state)" class="mono">{{ label(t, te, "sagaState", inv.saga_state) }}</Badge>
      </div>
    </Card>
    <Card class="gap-1 text-sm">
      <span class="font-medium">{{ label(t, te, "invoiceKind", inv.kind) }}</span>
      <span>{{ inv.period_id }}</span>
      <span>{{ inv.customer_external_id }}</span>
    </Card>
  </div>

  <TransferCard v-if="inv && transfer && transfer.state !== 'none'" :transfer="transfer" @confirm="recordOpen = true" @reject="askReject" />
  <ManualPaymentsCard v-if="inv && (inv.payments ?? []).length > 0" :payments="inv.payments ?? []" :currency="inv.currency ?? ''" />
  <RecordPaymentDialog v-if="inv" v-model:open="recordOpen" :tenant-id="tenantId" :invoice-id="invoiceId" :currency="inv.currency ?? ''" :outstanding-minor="outstanding" :transfer="transferCharge" />
  <RejectTransferDialog v-model:open="rejectOpen" :tenant-id="tenantId" :invoice-id="invoiceId" :outcome="rejectOutcome" />

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("invoice.lines") }}</h2>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("invoice.lineColumns.no") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.kind") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.description") }}</TableHead>
          <TableHead>{{ t("invoice.lineColumns.period") }}</TableHead>
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
          <TableCell class="text-muted-foreground text-xs whitespace-nowrap">{{ day(line.service_from) }}<template v-if="line.service_from"> – {{ day(line.service_to) }}</template></TableCell>
          <TableCell>{{ line.meter_code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ line.quantity }}</TableCell>
          <TableCell class="text-end font-medium tabular-nums">{{ formatMinor(line.amount_minor, inv?.currency, locale) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>

  <Card v-if="inv && inv.number" class="mb-6" role="region" :aria-label="t('invoice.share.title')">
    <h2 class="text-lg font-semibold">{{ t("invoice.share.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("invoice.share.hint") }}</p>
    <form class="flex flex-wrap items-center gap-3" @submit.prevent="makeLink">
      <Input v-model="linkDays" inputmode="numeric" class="w-28" :aria-label="t('invoice.share.days')" />
      <Button type="submit" :disabled="links.create.isPending.value">{{ t("invoice.share.create") }}</Button>
      <Button type="button" variant="outline" :disabled="links.revoke.isPending.value" @click="links.revoke.mutate()">{{ t("invoice.share.revoke") }}</Button>
    </form>
    <div v-if="shareUrl" class="flex flex-wrap items-center gap-2">
      <Input :model-value="shareUrl" readonly class="mono max-w-xl" :aria-label="t('invoice.share.link')" @focus="($event.target as HTMLInputElement).select()" />
      <Button type="button" variant="outline" @click="copy">{{ t("invoice.share.copy") }}</Button>
    </div>
    <Link to="/tenants/$id/payment-links" :params="{ id: tenantId }" :search="{ invoice: invoiceId } as never" class="text-sm underline">{{ t("invoice.share.paymentLink") }}</Link>
    <p v-if="links.revoke.isSuccess.value" class="text-success text-sm" role="status">{{ t("invoice.share.revoked") }}</p>
    <p v-if="linkError" class="text-destructive text-sm" role="alert">{{ linkError }}</p>
    <p v-if="links.create.error.value" class="text-destructive text-sm" role="alert">{{ errorText(links.create.error.value) }}</p>
  </Card>

  <EInvoiceStatus v-if="inv && inv.number" :tenant-id="tenantId" :invoice-id="invoiceId" boxed />

  <CreditNotesCard v-if="inv && inv.status === 'paid'" :tenant-id="tenantId" :invoice-id="invoiceId" :currency="inv.currency" issuable />
  <CreditNotesCard v-else-if="inv" :tenant-id="tenantId" :invoice-id="invoiceId" :currency="inv.currency" />

  <Card class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("invoice.saga") }}</h2>
    <p v-if="saga.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(saga.error.value) }}</p>
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
          <TableCell><Badge :variant="sagaVariant(e.to_state)" class="mono">{{ label(t, te, "sagaState", e.to_state) }}</Badge></TableCell>
          <TableCell class="mono text-xs">{{ e.cause }}</TableCell>
          <TableCell class="mono text-muted-foreground text-xs">{{ e.at }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
