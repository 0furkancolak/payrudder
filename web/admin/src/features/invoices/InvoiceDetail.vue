<script setup lang="ts">
import { Banknote } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import PaymentLinkSheet from "@/features/paymentlinks/PaymentLinkSheet.vue";
import { useFormat } from "@/shared/lib/format";
import { capFirst, label } from "@/shared/lib/labels";
import { names, sheetName } from "@/shared/lib/sheets";
import { validLinkDays } from "@/shared/lib/rules";
import { absoluteUrl } from "@/shared/lib/url";
import { CopyField } from "@/shared/ui/copy-field";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { Button } from "@/shared/ui/button";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import EInvoiceStatus from "@/features/einvoice/EInvoiceStatus.vue";
import CreditNotesCard from "@/features/creditnotes/CreditNotesCard.vue";
import { InvoiceRelated } from "@/features/related";
import { Input } from "@/shared/ui/input";
import { useInvoice, useInvoiceLinks, useInvoiceSaga, useOpenPdf } from "./api";
import InvoiceHeadline from "./InvoiceHeadline.vue";
import InvoiceLines from "./InvoiceLines.vue";
import ManualPaymentsCard from "./ManualPaymentsCard.vue";
import { canRecordPayment, isTransferCharge, outstandingAfter, wantsTransferState } from "./payment";
import { useInvoiceTransfer } from "./paymentApi";
import RecordPaymentDialog from "./RecordPaymentDialog.vue";
import RejectTransferDialog from "./RejectTransferDialog.vue";
import TransferCard from "./TransferCard.vue";

/** Everything about one invoice: what to do with it, its figures, payments, lines, the shared link, the e-document, credit notes, the records related to it and the saga. It is the body of the sheet the invoices list opens. */
const { t, te, locale } = useI18n();
const fmt = useFormat();
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const tenantId = computed(() => props.tenantId);
const invoiceId = computed(() => props.invoiceId);

const invoice = useInvoice(tenantId, invoiceId);
const saga = useInvoiceSaga(tenantId, invoiceId);
const pdf = useOpenPdf(tenantId, invoiceId);
const links = useInvoiceLinks(tenantId, invoiceId);
// The payment link form opens over this sheet, with the invoice filled in; the payment links page is not left for it.
const paying = ref(false);
const linkDays = ref("90");
const linkError = ref("");
function makeLink() {
  linkError.value = "";
  const days = Number(linkDays.value);
  if (!validLinkDays(days)) {
    linkError.value = t("invoice.share.badDays");
    return;
  }
  links.create.mutate(days);
}
const shareUrl = computed(() => absoluteUrl(links.create.data.value?.url, window.location.origin));
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
// The dialog's name in the address says which of the two it is (see RejectTransferDialog), so a reload opens the one that was open.
const rejectOutcome = ref<"rejected" | "expired">(names.value.includes(sheetName("expire-transfer", props.invoiceId)) ? "expired" : "rejected");
function askReject(outcome: "rejected" | "expired") {
  rejectOutcome.value = outcome;
  rejectOpen.value = true;
}
</script>

<template>
  <ErrorState :error="invoice.error.value" :retrying="invoice.isFetching.value" class="mb-4" @retry="invoice.refetch()" />

  <section v-if="inv" class="flex flex-col gap-6">
    <InvoiceHeadline :inv="inv" :pdf-pending="pdf.isPending.value" :pdf-error="pdf.error.value" @open-pdf="pdf.mutate()">
      <template #pills><StatusPill :tone="toneOf(inv.saga_state)">{{ capFirst(label(t, te, "sagaState", inv.saga_state), locale) }}</StatusPill></template>
      <template v-if="canRecord" #actions><Button type="button" variant="outline" @click="recordOpen = true"><Banknote />{{ t("invoice.payment.record") }}</Button></template>
    </InvoiceHeadline>

    <TransferCard v-if="transfer && transfer.state !== 'none'" :transfer="transfer" @confirm="recordOpen = true" @reject="askReject" />
    <ManualPaymentsCard v-if="(inv.payments ?? []).length > 0" :payments="inv.payments ?? []" :currency="inv.currency ?? ''" />

    <InvoiceLines :inv="inv" />

    <section v-if="inv.number" class="flex flex-col gap-3" :aria-label="t('invoice.share.title')">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("invoice.share.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("invoice.share.hint") }}</p>
      </div>
      <form class="flex flex-wrap items-center gap-3" @submit.prevent="makeLink">
        <Input v-model="linkDays" inputmode="numeric" class="w-28" :aria-label="t('invoice.share.days')" />
        <Button type="submit" :disabled="links.create.isPending.value">{{ t("invoice.share.create") }}</Button>
        <Button type="button" variant="outline" :disabled="links.revoke.isPending.value" @click="links.revoke.mutate()">{{ t("invoice.share.revoke") }}</Button>
      </form>
      <CopyField v-if="shareUrl" :value="shareUrl" :label="t('invoice.share.link')" :copy-label="t('invoice.share.copy')" />
      <button type="button" class="w-fit text-sm underline" @click="paying = true">{{ t("invoice.share.paymentLink") }}</button>
      <PaymentLinkSheet v-model:open="paying" :tenant-id="tenantId" :invoice="invoiceId" :customer="inv.customer_external_id ?? ''" />
      <p v-if="links.revoke.isSuccess.value" class="text-success text-sm" role="status">{{ t("invoice.share.revoked") }}</p>
      <ErrorText :message="linkError" />
      <ErrorText :error="links.create.error.value" />
    </section>

    <EInvoiceStatus v-if="inv.number" :tenant-id="tenantId" :invoice-id="invoiceId" />

    <CreditNotesCard :tenant-id="tenantId" :invoice-id="invoiceId" :currency="inv.currency" :issuable="inv.status === 'paid'" />

    <section class="flex flex-col gap-3" :aria-label="t('related.title')">
      <h2 class="text-lg font-semibold tracking-tight">{{ t("related.title") }}</h2>
      <InvoiceRelated :tenant-id="tenantId" :invoice-id="invoiceId" :customer="inv.customer_external_id" :credit-notes="false" />
    </section>

    <section class="flex flex-col gap-3" :aria-label="t('invoice.saga')">
      <h2 class="text-lg font-semibold tracking-tight">{{ t("invoice.saga") }}</h2>
      <ErrorText :error="saga.error.value" />
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
          <TableRow v-for="e in saga.data.value?.events ?? []" :key="e.seq" class="hover:bg-transparent" data-testid="saga-event">
            <TableCell class="text-end tabular-nums">{{ e.seq }}</TableCell>
            <TableCell class="text-xs">{{ label(t, te, "sagaState", e.from_state) }}</TableCell>
            <TableCell><StatusPill :tone="toneOf(e.to_state)">{{ capFirst(label(t, te, "sagaState", e.to_state), locale) }}</StatusPill></TableCell>
            <TableCell class="mono text-xs">{{ e.cause }}</TableCell>
            <TableCell class="text-muted-foreground text-xs whitespace-nowrap">{{ fmt.date(e.at, "dateTime") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </section>
  </section>

  <RecordPaymentDialog v-if="inv" v-model:open="recordOpen" :tenant-id="tenantId" :invoice-id="invoiceId" :currency="inv.currency ?? ''" :outstanding-minor="outstanding" :transfer="transferCharge" />
  <RejectTransferDialog v-model:open="rejectOpen" :tenant-id="tenantId" :invoice-id="invoiceId" :outcome="rejectOutcome" />
</template>
