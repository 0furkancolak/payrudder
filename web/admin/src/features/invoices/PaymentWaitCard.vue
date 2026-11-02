<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useCanWrite } from "@/shared/lib/access";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { DetailList, type DetailRow } from "@/shared/ui/detail-list";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { StatusPill } from "@/shared/ui/status-pill";
import { outstandingAfter } from "./payment";
import { ACTION_REQUIRED, HELD, heldActions, holdReason, releaseOutcome, waits } from "./paymentWait";
import { useHeldInvoiceActions, useInvoicePaymentWait } from "./waitApi";

/**
 * What an invoice's payment is waiting for when the wait is Payrudder's or the customer's to end, on the invoice's sheet (shown only then).
 * Held first charge: why (the payment gate, asked now), since when, when Payrudder looks again; an owner or an administrator can release it (the gate is asked again; it is
 * never overridden) or void it, each after a question that names the amount. Payment that needs the customer: that they were mailed, since when, when the next check is and when
 * the payment counts as declined. A viewer is told and given no button; the operator's environment on hold gives none either (the server refuses every write then).
 */
const props = defineProps<{
  tenantId: string;
  invoiceId: string;
  inv: { number?: string; total_minor?: string; currency?: string; customer_external_id?: string; saga_state?: string; payments?: { amount_minor?: string }[] };
}>();
const { t, locale } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const wait = useInvoicePaymentWait(() => props.tenantId, () => props.invoiceId, () => waits(props.inv.saga_state));
const actions = useHeldInvoiceActions(() => props.tenantId, () => props.invoiceId);
const w = computed(() => wait.data.value);
const when = (v?: string | null) => fmt.date(v, "dateTime");
const amount = computed(() => formatMinor(outstandingAfter(props.inv.total_minor, props.inv.payments), props.inv.currency, locale.value));
const number = computed(() => props.inv.number || t("product.billing.invoices.draft"));
const may = computed(() => (w.value ? heldActions(w.value, canWrite.value) : { release: false, void: false }));
const error = computed(() => actions.release.error.value ?? actions.voidIt.error.value);

const rows = computed<DetailRow[]>(() => {
  const v = w.value;
  if (!v) return [];
  if (v.state === HELD) return [{ key: "since", label: t("invoice.wait.since"), value: when(v.since_time) }, { key: "next", label: t("invoice.wait.next"), value: when(v.next_check_time) }];
  return [
    { key: "mailed", label: t("invoice.wait.mailed"), value: when(v.since_time) },
    { key: "next", label: t("invoice.wait.nextAsk"), value: when(v.next_check_time) },
    { key: "ends", label: t("invoice.wait.windowEnds"), value: v.window_ends_time ? when(v.window_ends_time) : "" },
  ];
});

async function release() {
  const ok = await askConfirm({
    title: t("invoice.wait.confirmRelease.title", { number: number.value }),
    body: t("invoice.wait.confirmRelease.body", { amount: amount.value, customer: props.inv.customer_external_id ?? "" }),
    confirmLabel: t("invoice.wait.confirmRelease.accept"),
    destructive: false,
  });
  if (!ok) return;
  try {
    const saga = await actions.release.mutateAsync();
    toast(t(`invoice.wait.${releaseOutcome(saga.state)}`, { amount: amount.value }));
  } catch {
    // The mutation's error is shown under the buttons.
  }
}

async function voidIt() {
  const ok = await askConfirm({
    title: t("invoice.wait.confirmVoid.title", { number: number.value }),
    body: t("invoice.wait.confirmVoid.body", { amount: amount.value }),
    confirmLabel: t("invoice.wait.confirmVoid.accept"),
  });
  if (!ok) return;
  try {
    await actions.voidIt.mutateAsync();
    toast.success(t("invoice.wait.voided"));
  } catch {
    // The mutation's error is shown under the buttons.
  }
}
</script>

<template>
  <ErrorState :error="wait.error.value" :retrying="wait.isFetching.value" compact @retry="wait.refetch()" />
  <section v-if="w" class="bg-muted/50 flex flex-col gap-3 rounded-2xl p-4" :aria-label="t(w.state === HELD ? 'invoice.wait.heldTitle' : 'invoice.wait.actionTitle')" data-testid="payment-wait">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="m-0 text-lg font-semibold tracking-tight">{{ t(w.state === ACTION_REQUIRED ? "invoice.wait.actionTitle" : "invoice.wait.heldTitle") }}</h2>
      <StatusPill tone="warning">{{ t(w.state === ACTION_REQUIRED ? "invoice.wait.actionPill" : "invoice.wait.heldPill") }}</StatusPill>
    </div>
    <template v-if="w.state === HELD">
      <p class="text-muted-foreground m-0 max-w-prose text-sm">{{ t("invoice.wait.heldHint") }}</p>
      <p class="m-0 max-w-prose text-sm font-medium" data-testid="hold-reason" :data-reason="holdReason(w)">{{ t(`invoice.wait.reason.${holdReason(w)}`) }}</p>
    </template>
    <template v-else>
      <p class="text-muted-foreground m-0 max-w-prose text-sm">{{ t("invoice.wait.actionHint") }}</p>
      <p class="text-muted-foreground m-0 max-w-prose text-sm">{{ t("invoice.wait.mailedHint") }}</p>
    </template>
    <DetailList :rows="rows" />
    <p v-if="w.state === ACTION_REQUIRED" class="text-muted-foreground m-0 max-w-prose text-sm">{{ t("invoice.wait.windowHint") }}</p>
    <div v-if="may.release" class="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" :disabled="actions.release.isPending.value || actions.voidIt.isPending.value" data-testid="held-release" @click="release">{{ t("invoice.wait.release") }}</Button>
      <Button type="button" variant="outline" :disabled="actions.release.isPending.value || actions.voidIt.isPending.value" data-testid="held-void" @click="voidIt">{{ t("invoice.wait.void") }}</Button>
    </div>
    <p v-else-if="w.state === HELD && holdReason(w) !== 'tenant_on_hold'" class="text-muted-foreground m-0 text-xs" data-testid="held-viewer">{{ t("invoice.wait.viewerNote") }}</p>
    <ErrorText :error="error" />
  </section>
</template>
