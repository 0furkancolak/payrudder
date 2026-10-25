<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { useFormat } from "@/shared/lib/format";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { useEInvoiceActions, useEInvoiceDocuments } from "./api";
import { docActions, docVariant, errorKeyPath } from "./status";

/** The legal e-document of an invoice or of a credit note: its state, legal number, and what an operator can do with it. Nothing is drawn when there is none. */
const props = defineProps<{ tenantId: string; invoiceId?: string; creditNoteId?: string; boxed?: boolean }>();
const { t, te } = useI18n();
const fmt = useFormat();
const docs = useEInvoiceDocuments(
  () => props.tenantId,
  () => ({ invoiceId: props.invoiceId, creditNoteId: props.creditNoteId }),
);
const actions = useEInvoiceActions(() => props.tenantId);
const list = computed(() => docs.data.value ?? []);
const when = (v?: string | null) => fmt.date(v, "dateTime");
/** The translated error of an error key, the server's English text when the console has none. */
function docErrorText(d: { last_error_key?: string; last_error?: string }) {
  const path = errorKeyPath(d.last_error_key);
  return path && te(path) ? t(path) : (d.last_error ?? "");
}
function cancel(id: string) {
  if (window.confirm(t("einvoice.confirmCancel"))) actions.cancel.mutate(id);
}
const failure = computed(() => firstError(actions.retry, actions.cancel, actions.download));
</script>

<template>
  <div v-if="list.length" :class="['grid gap-2 text-sm', boxed && 'bg-card mb-6 rounded-xl border p-4 shadow-sm']" role="region" :aria-label="t('einvoice.title')" data-testid="einvoice-status">
    <div v-for="d in list" :key="d.id" class="grid gap-1">
      <div class="flex flex-wrap items-center gap-2">
        <span class="font-medium">{{ t("einvoice.title") }}</span>
        <Badge :variant="docVariant(d.status)" data-testid="einvoice-badge">{{ t(`einvoice.status.${d.status}`) }}</Badge>
        <Badge v-if="d.document_type" variant="outline">{{ t(`einvoice.type.${d.document_type}`) }}</Badge>
        <span v-if="d.legal_number" class="mono text-xs" data-testid="einvoice-number">{{ d.legal_number }}</span>
      </div>
      <p v-if="d.status === 'pending' && d.attempts" class="text-muted-foreground text-xs">
        {{ t("einvoice.attempts", { count: d.attempts }) }}<template v-if="d.next_attempt_at"> · {{ t("einvoice.nextAttempt", { at: when(d.next_attempt_at) }) }}</template>
      </p>
      <p v-if="d.status === 'issued' && d.issued_at" class="text-muted-foreground text-xs">{{ t("einvoice.issuedAt", { at: when(d.issued_at) }) }}</p>
      <p v-if="d.status === 'failed' || d.last_error" class="text-destructive text-xs" role="alert">{{ docErrorText(d) }}</p>
      <div class="flex flex-wrap gap-2">
        <Button v-if="docActions(d).retry" size="sm" variant="outline" :disabled="actions.retry.isPending.value" @click="actions.retry.mutate(d.id ?? '')">{{ t("einvoice.retry") }}</Button>
        <Button v-if="docActions(d).cancel" size="sm" variant="outline" :disabled="actions.cancel.isPending.value" @click="cancel(d.id ?? '')">{{ t("einvoice.cancel") }}</Button>
        <Button v-if="d.has_xml" size="sm" variant="ghost" :disabled="actions.download.isPending.value" @click="actions.download.mutate({ id: d.id ?? '', kind: 'xml' })">{{ t("einvoice.downloadXml") }}</Button>
        <Button v-if="d.has_pdf" size="sm" variant="ghost" :disabled="actions.download.isPending.value" @click="actions.download.mutate({ id: d.id ?? '', kind: 'pdf' })">{{ t("einvoice.downloadPdf") }}</Button>
      </div>
    </div>
    <ErrorText :error="failure" class="text-xs" />
  </div>
</template>
