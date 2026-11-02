<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { DetailList, type DetailRow } from "@/shared/ui/detail-list";
import { ErrorState } from "@/shared/ui/error-state";
import { StatusPill } from "@/shared/ui/status-pill";
import { useDisputes } from "./api";
import DisputeNotes from "./DisputeNotes.vue";
import { disputeKind, disputeTone, dueShown, dueState, meaningKey, type Dispute } from "./dispute";

/**
 * The cases customers opened with their bank about this invoice's payment, on the invoice's sheet: shown only when there are any. Each says whether it is a chargeback
 * (the provider has taken the money back until it decides) or an inquiry, where it stands, when the seller's evidence is due while it is open (and that it is close or past),
 * what the dispute cost, and the seller's own notes and mark that it was handled.
 */
const props = defineProps<{ tenantId: string; invoiceId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const disputes = useDisputes(() => props.tenantId, () => props.invoiceId);
const rows = computed(() => disputes.data.value?.disputes ?? []);
const when = (v?: string) => fmt.date(v, "dateTime");
const dueText = (d: Dispute) => {
  const state = dueState(d, new Date());
  return when(dueShown(d)) + (state === "overdue" || state === "soon" ? ` · ${t(`disputes.due.${state}`)}` : "");
};
const detail = (d: Dispute): DetailRow[] => [
  { key: "provider", label: t("disputes.provider"), value: label(t, te, "provider", d.provider) },
  { key: "reason", label: t("disputes.reason"), value: d.reason ? label(t, te, "disputeReason", d.reason) : "" },
  ...(dueShown(d) ? [{ key: "due", label: t("disputes.evidenceDue"), value: dueText(d) }] : []),
  ...(d.fee_minor ? [{ key: "fee", label: t("disputes.fee"), value: formatMinor(d.fee_minor, d.fee_currency, locale.value) }] : []),
  { key: "opened", label: t("disputes.opened"), value: when(d.opened_time) },
  ...(d.closed_time ? [{ key: "closed", label: t("disputes.closed"), value: when(d.closed_time) }] : []),
  { key: "ref", label: t("disputes.reference"), value: d.dispute_id, mono: true },
];
</script>

<template>
  <ErrorState :error="disputes.error.value" :retrying="disputes.isFetching.value" compact @retry="disputes.refetch()" />
  <section v-if="rows.length > 0" class="flex flex-col gap-3" :aria-label="t('disputes.title')" data-testid="dispute-card">
    <div>
      <h2 class="text-lg font-semibold tracking-tight">{{ t("disputes.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("disputes.hint") }}</p>
    </div>
    <ul class="flex flex-col gap-3">
      <li v-for="d in rows" :key="d.id" class="flex flex-col gap-3 rounded-2xl border p-4" data-testid="dispute">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <StatusPill :tone="disputeTone(d.outcome)" data-testid="dispute-outcome">{{ t(`disputes.outcome.${d.outcome}`) }}</StatusPill>
          <span class="text-sm font-medium">{{ t(`disputes.kind.${disputeKind(d)}`) }}</span>
          <StatusPill v-if="d.handled" tone="info" data-testid="dispute-handled-pill">{{ t("disputes.handled") }}</StatusPill>
          <span class="ms-auto font-medium tabular-nums">{{ formatMinor(d.amount_minor, d.currency, locale) }}</span>
        </div>
        <p class="text-sm" data-testid="dispute-meaning">{{ t(`disputes.means.${meaningKey(d)}`) }}</p>
        <DetailList :rows="detail(d)" />
        <DisputeNotes :tenant-id="tenantId" :dispute="d" />
      </li>
    </ul>
  </section>
</template>
