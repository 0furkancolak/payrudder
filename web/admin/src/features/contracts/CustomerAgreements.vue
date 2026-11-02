<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Eraser, FilePlus } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { tenantBase } from "@/shared/lib/session";
import { Button } from "@/shared/ui/button";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useContractAcceptances, useContractWithdrawals } from "./api";
import { LANGUAGE_NAMES, evidenceValue, groupAcceptances, witnessOf, type AcceptanceGroup, type ContractAcceptance, type WithdrawalStatus } from "./lib";
import RecordWithdrawalSheet from "./RecordWithdrawalSheet.vue";
import SnapshotDialog from "./SnapshotDialog.vue";

/**
 * What a customer agreed to, for their sheet: every moment they accepted agreements (the versions together, in the language they were shown, where it
 * happened), what Payrudder saw itself and what the seller reported, the two separate consents when the service starts at once, the address and browser
 * (blank once the retention ran out), and the page they accepted, opened in a sandboxed frame. Below it, their withdrawal requests, and a way to record one.
 * `readonly` (the sheet opened over another one) shows all of it and offers no form.
 */
const props = defineProps<{ tenantId: string; externalId: string; readonly?: boolean }>();
const { t } = useI18n();
const fmt = useFormat();
const acceptances = useContractAcceptances(() => props.tenantId, () => props.externalId);
const withdrawals = useContractWithdrawals(() => props.tenantId, () => props.externalId);
const groups = computed(() => groupAcceptances(acceptances.data.value));
const viewing = ref("");
const recording = ref(false);
const TONES = { received: "warning", accepted: "info", rejected: "neutral", refunded: "success" } as const satisfies Record<WithdrawalStatus, string>;

const languageText = (a: ContractAcceptance): string =>
  a.locale_requested !== "" && a.locale_requested !== a.locale_served
    ? t("contracts.customer.servedAs", { served: LANGUAGE_NAMES[a.locale_served as keyof typeof LANGUAGE_NAMES] ?? a.locale_served, asked: LANGUAGE_NAMES[a.locale_requested as keyof typeof LANGUAGE_NAMES] ?? a.locale_requested })
    : (LANGUAGE_NAMES[a.locale_served as keyof typeof LANGUAGE_NAMES] ?? a.locale_served);
const FIELDS = ["ip", "user_agent"] as const;
const first = (g: AcceptanceGroup): ContractAcceptance => g.anchor;
</script>

<template>
  <div class="flex flex-col gap-6" data-testid="customer-agreements">
    <section class="flex flex-col gap-3" :aria-label="t('contracts.customer.log')">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="m-0 text-sm font-semibold">{{ t("contracts.customer.log") }}</h3>
      </div>
      <ErrorState :error="acceptances.error.value" :retrying="acceptances.isFetching.value" compact @retry="acceptances.refetch()" />
      <Skeleton v-if="acceptances.isPending.value" class="h-24 rounded-2xl" />
      <p v-else-if="groups.length === 0 && !acceptances.error.value" class="text-muted-foreground m-0 text-sm" data-testid="acceptances-empty">{{ t("contracts.customer.none") }}</p>
      <ol v-else class="m-0 flex list-none flex-col gap-3 p-0">
        <li v-for="g in groups" :key="g.id" class="border-border/70 flex flex-col gap-3 rounded-2xl border p-4" data-testid="acceptance-group" :data-group="g.id">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ fmt.date(g.time, "dateTime") }}</span>
              <StatusPill :tone="g.source === 'hosted' ? 'success' : 'info'" data-testid="acceptance-source">{{ g.source === "hosted" ? t("contracts.customer.sourceHosted") : t("contracts.customer.sourceApi") }}</StatusPill>
            </div>
            <Button size="sm" variant="outline" data-testid="acceptance-view" @click="viewing = g.anchor.id">{{ t("contracts.customer.viewPage") }}</Button>
          </div>

          <ul class="m-0 flex list-none flex-col gap-2 p-0 text-sm">
            <li v-for="a in g.items" :key="a.id" class="flex flex-col gap-0.5" data-testid="acceptance-item">
              <span class="font-medium">{{ t(`contracts.kinds.${a.kind}`) }} · {{ t("contracts.history.version", { n: a.version_number }) }} · <span class="text-muted-foreground font-normal">{{ languageText(a) }}</span></span>
              <span class="text-muted-foreground text-xs break-words" dir="auto">{{ t("contracts.customer.box", { text: a.checkbox_text }) }}</span>
            </li>
          </ul>

          <ul v-if="first(g).immediate_start_consent || first(g).withdrawal_waiver_acknowledged" class="m-0 flex list-none flex-col gap-1.5 p-0 text-sm" data-testid="acceptance-consents">
            <li v-if="first(g).immediate_start_consent" class="flex flex-col"><span class="font-medium">{{ t("contracts.customer.consentStart") }}</span><span class="text-muted-foreground text-xs break-words" dir="auto">{{ first(g).immediate_start_text }}</span></li>
            <li v-if="first(g).withdrawal_waiver_acknowledged" class="flex flex-col"><span class="font-medium">{{ t("contracts.customer.consentWaiver") }}</span><span class="text-muted-foreground text-xs break-words" dir="auto">{{ first(g).withdrawal_waiver_text }}</span></li>
          </ul>

          <dl class="border-border/70 m-0 grid gap-1.5 border-t pt-3 text-sm sm:grid-cols-[10rem_1fr]" :aria-label="t('contracts.customer.evidence')">
            <dt class="text-muted-foreground">{{ t("contracts.customer.time") }}</dt>
            <dd class="m-0 flex flex-wrap items-center gap-2">{{ fmt.date(first(g).accept_time, "shortSeconds") }}<StatusPill :tone="witnessOf(first(g), 'time') === 'observed' ? 'success' : 'neutral'" data-field="time">{{ t(`contracts.customer.${witnessOf(first(g), "time")}`) }}</StatusPill></dd>
            <dt class="text-muted-foreground">{{ t("contracts.customer.words") }}</dt>
            <dd class="m-0 flex flex-wrap items-center gap-2"><StatusPill :tone="witnessOf(first(g), 'text') === 'observed' ? 'success' : 'neutral'" data-field="text">{{ t(`contracts.customer.${witnessOf(first(g), "text")}`) }}</StatusPill></dd>
            <template v-for="f in FIELDS" :key="f">
              <dt class="text-muted-foreground">{{ t(f === "ip" ? "contracts.customer.ip" : "contracts.customer.userAgent") }}</dt>
              <dd class="m-0 flex min-w-0 flex-wrap items-center gap-2">
                <span v-if="evidenceValue(first(g), f).state === 'present'" class="mono break-all" :data-field-value="f">{{ evidenceValue(first(g), f).value }}</span>
                <span v-else-if="evidenceValue(first(g), f).state === 'erased'" class="text-muted-foreground inline-flex items-center gap-1" :data-field-value="f" data-state="erased"><Eraser class="size-3.5" aria-hidden="true" />{{ t("contracts.customer.erased") }}</span>
                <span v-else class="text-muted-foreground" :data-field-value="f" data-state="none">{{ t("contracts.customer.notReported") }}</span>
                <StatusPill :tone="witnessOf(first(g), f) === 'observed' ? 'success' : 'neutral'" :data-field="f">{{ t(`contracts.customer.${witnessOf(first(g), f)}`) }}</StatusPill>
              </dd>
            </template>
          </dl>
        </li>
      </ol>
    </section>

    <section class="flex flex-col gap-3" :aria-label="t('contracts.customer.withdrawals')">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="m-0 text-sm font-semibold">{{ t("contracts.customer.withdrawals") }}</h3>
        <Button v-if="!readonly" size="sm" variant="outline" data-testid="withdrawal-record" @click="recording = true"><FilePlus aria-hidden="true" />{{ t("contracts.customer.record") }}</Button>
      </div>
      <ErrorState :error="withdrawals.error.value" :retrying="withdrawals.isFetching.value" compact @retry="withdrawals.refetch()" />
      <Skeleton v-if="withdrawals.isPending.value" class="h-12 rounded-2xl" />
      <p v-else-if="(withdrawals.data.value ?? []).length === 0 && !withdrawals.error.value" class="text-muted-foreground m-0 text-sm">{{ t("contracts.customer.noWithdrawals") }}</p>
      <ul v-else class="divide-border/70 m-0 flex list-none flex-col divide-y p-0 text-sm">
        <li v-for="w in withdrawals.data.value" :key="w.id" class="flex flex-col gap-1 py-2.5" data-testid="customer-withdrawal">
          <div class="flex flex-wrap items-center gap-2">
            <StatusPill :tone="TONES[w.status]">{{ t(`contracts.withdrawals.statuses.${w.status}`) }}</StatusPill>
            <span class="text-muted-foreground text-xs">{{ fmt.date(w.receive_time, "dateTime") }} · {{ t(`contracts.withdrawals.sources.${w.source}`) }}</span>
          </div>
          <span v-if="w.statement" class="break-words" dir="auto">{{ w.statement }}</span>
          <span v-if="w.decide_time" class="text-muted-foreground text-xs break-words" dir="auto">{{ fmt.date(w.decide_time, "dateTime") }}<template v-if="w.decided_by"> · {{ t("contracts.agreements.by", { author: w.decided_by }) }}</template><template v-if="w.decision_note"> · {{ w.decision_note }}</template></span>
        </li>
      </ul>
      <Link v-if="!readonly" :to="`${tenantBase()}/$id/contracts`" :params="{ id: tenantId } as never" :search="{ tab: 'withdrawals' } as never" class="text-muted-foreground w-fit text-xs underline" data-testid="customer-withdrawals-link">{{ t("contracts.customer.decideThere") }}</Link>
    </section>

    <SnapshotDialog v-model:acceptance="viewing" :tenant-id="tenantId" />
    <RecordWithdrawalSheet v-if="!readonly" v-model:open="recording" :tenant-id="tenantId" :customer="externalId" />
  </div>
</template>
