<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { newest, oldest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { Skeleton } from "@/shared/ui/skeleton";
import { SortSelect } from "@/shared/ui/sort-select";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { useContractSettings, useContractWithdrawals, useDecideWithdrawal, useRequestRefund } from "./api";
import { canRequestRefund, withdrawalMoves, type ContractWithdrawal, type WithdrawalStatus } from "./lib";

/**
 * The withdrawal requests of the seller's customers. The seller decides: a request that was received is accepted or rejected, once, and an accepted one
 * is marked refunded once the customer has their money. Payrudder keeps the statement, the decision, who made it and when, and sends the webhook. Accepting a
 * withdrawal that names an invoice also asks billing for the refund (a credit note that returns it to the card) when the settings say so, never before the
 * seller decided; the row shows what became of it, and a refund that was not asked for or failed can be asked for here. A payment that cannot go back through
 * Payrudder (a bank transfer) is marked for the seller to refund by hand.
 */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

const withdrawals = useContractWithdrawals(() => props.tenantId);
const decide = useDecideWithdrawal(() => props.tenantId);
const requestRefund = useRequestRefund(() => props.tenantId);
const settings = useContractSettings(() => props.tenantId);
const failure = computed(() => decide.error.value ?? requestRefund.error.value);
const list = computed(() => withdrawals.data.value ?? []);
const STATUSES: WithdrawalStatus[] = ["received", "accepted", "rejected", "refunded"];
const TONES: Record<WithdrawalStatus, Tone> = { received: "warning", accepted: "info", rejected: "neutral", refunded: "success" };
const REFUND_TONES: Record<ContractWithdrawal["refund_state"], Tone> = { none: "neutral", requesting: "warning", requested: "info", outside: "warning", failed: "danger" };

const table = useTableState<ContractWithdrawal>(list, {
  searchFields: (w) => [w.customer_external_id, w.statement, w.decided_by, w.decision_note],
  sorts: () => [newest(t, (w) => time(w.receive_time)), oldest(t, (w) => time(w.receive_time))],
  filters: { status: (w, v) => w.status === v },
  url: true,
});
const statusFilter = computed({ get: () => table.filters.value.status ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, status: v }) });
const statusOptions = computed(() => [{ value: "", label: t("common.all") }, ...STATUSES.map((s) => ({ value: s, label: t(`contracts.withdrawals.statuses.${s}`) }))]);

// The decision is taken in a dialog that says what it does: it is final (a received request is decided once) and a refund is the seller's own act.
type Action = "accepted" | "rejected" | "refunded" | "refund";
const deciding = ref<{ row: ContractWithdrawal; status: Action } | null>(null);
/** What accepting does about the money, said in the dialog: the setting, and whether the withdrawal names an invoice to refund. */
const acceptRefund = computed(() => {
  const d = deciding.value;
  if (!d || d.status !== "accepted") return "";
  if (d.row.invoice_id === "") return "noInvoice";
  return settings.data.value?.withdrawal_refund === "none" ? "left" : "asked";
});
const note = ref("");
const open = computed({
  get: () => deciding.value !== null,
  set: (v) => {
    if (!v) deciding.value = null;
  },
});
function ask(row: ContractWithdrawal, status: Action) {
  note.value = "";
  decide.reset();
  requestRefund.reset();
  deciding.value = { row, status };
}
function confirm() {
  const d = deciding.value;
  if (!d) return;
  if (d.status === "refund") requestRefund.mutate(d.row.id, { onSuccess: () => (deciding.value = null) });
  else decide.mutate({ id: d.row.id, status: d.status, note: note.value.trim() }, { onSuccess: () => (deciding.value = null) });
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="bg-muted text-muted-foreground rounded-2xl px-4 py-3 text-sm" data-testid="withdrawals-note">
      <p class="m-0">{{ t("contracts.withdrawals.intro") }}</p>
      <p class="m-0 mt-1 font-medium">{{ t("contracts.withdrawals.refundNote") }}</p>
    </div>

    <Card class="p-3" data-testid="contracts-withdrawals">
      <ErrorState :error="withdrawals.error.value" :retrying="withdrawals.isFetching.value" compact @retry="withdrawals.refetch()" />
      <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('contracts.withdrawals.search')" @clear="table.clear()">
        <FilterSelect v-model="statusFilter" multiple :label="t('contracts.withdrawals.columns.status')" :options="statusOptions" />
        <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      </DataToolbar>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("contracts.withdrawals.columns.customer") }}</TableHead>
            <TableHead class="max-md:hidden">{{ t("contracts.withdrawals.columns.statement") }}</TableHead>
            <TableHead>{{ t("contracts.withdrawals.columns.received") }}</TableHead>
            <TableHead>{{ t("contracts.withdrawals.columns.status") }}</TableHead>
            <TableHead class="max-xl:hidden">{{ t("contracts.withdrawals.columns.decision") }}</TableHead>
            <TableHead class="max-lg:hidden">{{ t("contracts.withdrawals.columns.refund") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="w in table.items.value" :key="w.id" :data-testid="`withdrawal-${w.id}`" :data-status="w.status">
            <TableCell>
              <span class="mono font-medium break-all">{{ w.customer_external_id }}</span>
              <span class="text-muted-foreground block text-xs">{{ t(`contracts.withdrawals.sources.${w.source}`) }}</span>
            </TableCell>
            <TableCell class="max-w-xs whitespace-normal max-md:hidden"><span class="line-clamp-3 break-words" dir="auto" :title="w.statement">{{ w.statement || "—" }}</span></TableCell>
            <TableCell class="text-muted-foreground whitespace-nowrap">{{ fmt.date(w.receive_time, "dateTime") }}</TableCell>
            <TableCell><StatusPill :tone="TONES[w.status]" data-testid="withdrawal-status">{{ t(`contracts.withdrawals.statuses.${w.status}`) }}</StatusPill></TableCell>
            <TableCell class="text-muted-foreground max-w-xs text-xs whitespace-normal max-xl:hidden">
              <template v-if="w.decide_time">
                {{ fmt.date(w.decide_time, "dateTime") }}<template v-if="w.decided_by"> · {{ t("contracts.agreements.by", { author: w.decided_by }) }}</template>
                <span v-if="w.decision_note" class="block break-words" dir="auto">{{ w.decision_note }}</span>
              </template>
              <template v-else>—</template>
            </TableCell>
            <TableCell class="max-w-xs text-xs whitespace-normal max-lg:hidden" :data-refund="w.refund_state">
              <template v-if="w.status === 'accepted' || w.status === 'refunded'">
                <StatusPill :tone="REFUND_TONES[w.refund_state]" data-testid="withdrawal-refund-state">{{ t(`contracts.withdrawals.refundStates.${w.refund_state}`) }}</StatusPill>
                <span v-if="w.credit_note_id" class="text-muted-foreground mono block break-all" data-testid="withdrawal-credit-note">{{ t("contracts.withdrawals.creditNote", { id: w.credit_note_id }) }}</span>
                <span v-if="w.refund_note" class="text-muted-foreground block break-words" dir="auto">{{ w.refund_note }}</span>
              </template>
              <template v-else>—</template>
            </TableCell>
            <TableCell>
              <div class="flex flex-wrap justify-end gap-1.5">
                <Button v-if="canRequestRefund(w)" size="sm" variant="outline" :disabled="!canWrite" :data-testid="`withdrawal-refund-${w.id}`" @click="ask(w, 'refund')">{{ t("contracts.withdrawals.actions.refund") }}</Button>
                <Button v-for="s in withdrawalMoves(w.status)" :key="s" size="sm" :variant="s === 'rejected' ? 'outline' : 'default'" :disabled="!canWrite" :data-testid="`withdrawal-${s}-${w.id}`" @click="ask(w, s)">{{ t(`contracts.withdrawals.actions.${s}`) }}</Button>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <Skeleton v-if="withdrawals.isPending.value" class="mx-3 my-4 h-24" />
      <TableEmpty v-else-if="list.length === 0 && !withdrawals.error.value" data-testid="withdrawals-empty">{{ t("contracts.withdrawals.empty") }}</TableEmpty>
      <TableEmpty v-else-if="list.length > 0 && table.total.value === 0">{{ t("common.noMatches") }}</TableEmpty>
      <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
    </Card>

    <Dialog v-model:open="open">
      <DialogContent class="sm:max-w-md" data-testid="withdrawal-dialog">
        <DialogHeader>
          <DialogTitle>{{ deciding ? t(`contracts.withdrawals.decide.${deciding.status}.title`) : "" }}</DialogTitle>
          <DialogDescription>{{ deciding ? t(`contracts.withdrawals.decide.${deciding.status}.body`) : "" }}</DialogDescription>
        </DialogHeader>
        <p v-if="acceptRefund" class="bg-muted rounded-xl px-3 py-2 text-sm" data-testid="withdrawal-refund-effect">{{ t(`contracts.withdrawals.acceptRefund.${acceptRefund}`) }}</p>
        <FormField v-if="deciding?.status !== 'refund'" :label="t('contracts.withdrawals.note')" for="withdrawal-note" :hint="t('contracts.withdrawals.noteHint')">
          <Textarea id="withdrawal-note" v-model="note" maxlength="1000" data-testid="withdrawal-note" />
        </FormField>
        <ErrorText :error="failure" />
        <DialogFooter>
          <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="button" :disabled="decide.isPending.value || requestRefund.isPending.value" data-testid="withdrawal-confirm" @click="confirm">{{ deciding ? t(`contracts.withdrawals.actions.${deciding.status}`) : "" }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
