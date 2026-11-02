<script setup lang="ts">
import { CheckCheck, MessageSquareText, ArrowUpRight } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import PeekInvoice from "@/features/related/PeekInvoice.vue";
import { useTenant } from "@/features/tenants/api";
import { useBulk } from "@/shared/lib/bulk";
import { useFormat } from "@/shared/lib/format";
import { label } from "@/shared/lib/labels";
import { formatMinor } from "@/shared/lib/money";
import { amount, amountHigh, amountLow, newest, oldest, time } from "@/shared/lib/sortDefs";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { FormField } from "@/shared/ui/form-field";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { RowActions } from "@/shared/ui/row-actions";
import { SortSelect } from "@/shared/ui/sort-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { ym } from "@/shared/lib/dates";
import { useDiscrepancies, useDiscrepancy, useDiscrepancyActions, useRunReconciliation } from "./api";
import { actionable, environmentOf, KINDS, resolutionsFor, statusesOf, statusTone, STATUSES, VIEWS, type View } from "./reconcile";

/**
 * The review queue of reconciliation (docs/reconciliation.md): what a run found, kept, with an owner and a history. The queue loads what needs a person by default
 * (open and explained), or the resolved ones, or all, up to a page; the filters narrow what was loaded, and the actions are the operator's: explain, resolve,
 * assign. Resolving does not make a difference go away: a later run that still finds it reopens it.
 */
const props = defineProps<{ tenantId: string }>();
const { t, te, locale } = useI18n();
const fmt = useFormat();
const tenant = useTenant(() => props.tenantId);
const environment = computed(() => environmentOf(tenant.data.value?.environment));

const view = ref<View>("review");
const queue = useDiscrepancies(() => props.tenantId, () => statusesOf(view.value));
type Row = NonNullable<NonNullable<typeof queue.data.value>["discrepancies"]>[number];
const rows = computed(() => queue.data.value?.discrepancies ?? []);
const counts = computed(() => {
  const out: Record<string, number> = {};
  for (const c of queue.data.value?.counts ?? []) out[c.status] = (out[c.status] ?? 0) + Number(c.n);
  return out;
});
const lastRun = computed(() => queue.data.value?.last_run);
const money = (minor: number | undefined, currency: string | undefined) => (minor ? formatMinor(String(minor), currency, locale.value) : "");
const when = (v?: string) => fmt.date(v, "short");

// --- run
const period = ref(ym(new Date()));
const run = useRunReconciliation(() => props.tenantId);
const lastResult = computed(() => run.data.value);

// --- the list
const all = computed(() => ({ value: "", label: t("common.all") }));
const table = useTableState<Row>(rows, {
  selectId: (d) => String(d.id),
  url: "issues-", searchFields: (d) => [d.number, d.invoice_id, d.provider, d.provider_ref, d.detail, d.owner, d.note],
  sorts: () => [newest(t, (d) => time(d.first_seen_at)), oldest(t, (d) => time(d.first_seen_at)), amountHigh(t, (d) => amount(d.billed_minor ?? d.ledger_minor)), amountLow(t, (d) => amount(d.billed_minor ?? d.ledger_minor))],
  filters: { kind: (d, v) => d.kind === v, status: (d, v) => d.status === v },
});
const kindFilter = computed({ get: () => table.filters.value.kind ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, kind: v }) });
const statusFilter = computed({ get: () => table.filters.value.status ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, status: v }) });
const kindOptions = computed(() => [all.value, ...KINDS.map((k) => ({ value: k, label: label(t, te, "discrepancy", k) }))]);
const statusOptions = computed(() => [all.value, ...STATUSES.map((s) => ({ value: s, label: label(t, te, "discrepancyStatus", s) }))]);
const viewOptions = computed(() => VIEWS.map((v) => ({ value: v, label: t(`billing.reconcile.view.${v}`) })));
const viewDraft = computed({ get: () => view.value, set: (v: string) => (view.value = (VIEWS as readonly string[]).includes(v) ? (v as View) : "review") });
const current = table.row;
const more = computed(() => !!queue.data.value?.next_page_token);

// --- actions: one dialog for explaining and resolving, one or many
const actions = useDiscrepancyActions(() => props.tenantId);
const dialog = ref<{ mode: "explain" | "resolve"; rows: Row[]; ticked: boolean } | null>(null);
const form = ref({ resolution: "other", note: "", owner: "", provider: "", ref: "" });
const bulk = useBulk(table, { name: (d) => d.number || String(d.id) });
const error = computed(() => actions.explain.error.value ?? actions.resolve.error.value ?? actions.assign.error.value);

function open(mode: "explain" | "resolve", rows: Row[], ticked = false) {
  const first = rows[0];
  form.value = { resolution: resolutionsFor(first?.kind, rows.length > 1)[0] ?? "other", note: "", owner: rows.length === 1 ? (first?.owner ?? "") : "", provider: first?.provider ?? "", ref: "" };
  dialog.value = { mode, rows, ticked };
}
const choices = computed(() => resolutionsFor(dialog.value?.rows[0]?.kind, (dialog.value?.rows.length ?? 0) > 1));
const fact = computed(() => dialog.value?.mode === "resolve" && form.value.resolution === "ledger_fact");
const valid = computed(() => form.value.note.trim() !== "" && (!fact.value || (form.value.provider.trim() !== "" && form.value.ref.trim() !== "")));

async function submit() {
  const d = dialog.value;
  if (!d || !valid.value) return;
  const { resolution, note, owner, provider, ref } = form.value;
  const one = (r: Row) =>
    d.mode === "explain"
      ? actions.explain.mutateAsync({ id: r.id, note, owner: owner || undefined })
      : actions.resolve.mutateAsync({ id: r.id, resolution, note, owner: owner || undefined, ledgerFact: fact.value ? { provider: provider.trim(), provider_ref: ref.trim() } : undefined });
  dialog.value = null;
  if (!d.ticked) {
    // A row's own action acts on that row, whatever else is ticked.
    await one(d.rows[0] as Row).catch(() => undefined);
    return;
  }
  await bulk.run(one, { rows: (ticked) => ticked.filter(actionable), confirm: d.mode === "resolve" ? { title: t("billing.reconcile.confirmTitle", { n: d.rows.length }), body: t("billing.reconcile.confirmBody"), confirmLabel: t("billing.reconcile.resolve"), destructive: false } : undefined });
}
const rowActions = (d: Row) =>
  actionable(d)
    ? [
        { key: "explain", label: t("billing.reconcile.explain"), icon: MessageSquareText, onSelect: () => open("explain", [d]) },
        { key: "resolve", label: t("billing.reconcile.resolve"), icon: CheckCheck, onSelect: () => open("resolve", [d]) },
      ]
    : [];
const viewRow = (i: number) => [{ key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) }];

const detail = useDiscrepancy(() => props.tenantId, () => current.value?.id);
const events = computed(() => detail.data.value?.events ?? []);
const owner = ref("");
const detailRows = computed(() => {
  const d = current.value;
  if (!d) return [];
  return [
    { label: t("billing.payments.kind"), value: label(t, te, "discrepancy", d.kind) },
    { label: t("billing.payments.provider"), value: [label(t, te, "provider", d.provider), d.provider_ref].filter(Boolean).join(" ") },
    { label: t("billing.payments.billed"), value: money(d.billed_minor, d.currency) },
    { label: t("billing.payments.ledgerAmount"), value: money(d.ledger_minor, d.currency) },
    { label: t("billing.reconcile.settled"), value: money(d.settled_minor, d.currency) },
    { label: t("billing.reconcile.period"), value: d.period },
    { label: t("billing.reconcile.firstSeen"), value: when(d.first_seen_at) },
    { label: t("billing.reconcile.lastSeen"), value: when(d.last_seen_at) },
    { label: t("billing.reconcile.owner"), value: d.owner || "" },
    { label: t("billing.reconcile.note"), value: d.note || "" },
    { label: t("billing.reconcile.resolution"), value: d.resolution ? label(t, te, "resolution", d.resolution) : "" },
  ].filter((r) => r.value !== "");
});
</script>

<template>
  <Card class="mb-6" data-testid="review-queue">
    <div>
      <h2 class="text-lg font-semibold">{{ t("billing.reconcile.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.reconcile.hint") }}</p>
    </div>
    <form class="flex flex-wrap items-center gap-2" role="form" :aria-label="t('billing.payments.reconcile')" @submit.prevent="environment && run.mutate({ period, environment })">
      <Input v-model="period" class="w-40" :aria-label="t('billing.payments.period')" placeholder="2026-10" pattern="\d{4}-(0[1-9]|1[0-2])" required />
      <Button type="submit" :disabled="run.isPending.value || !environment">{{ t("billing.payments.run") }}</Button>
    </form>
    <ErrorText :error="run.error.value" />
    <p v-if="lastResult" class="text-sm" role="status" data-testid="run-result">
      {{ t("billing.reconcile.ran", { period: lastResult.period, found: lastResult.found, opened: lastResult.opened, reopened: lastResult.reopened, cleared: lastResult.cleared }) }}
      <span v-if="lastResult.truncated" class="text-muted-foreground">{{ t("billing.payments.truncated") }}</span>
    </p>
    <p v-if="lastRun" class="text-muted-foreground text-xs" data-testid="last-run">
      {{ t("billing.reconcile.lastRun", { when: when(lastRun.finished_at), by: t(`billing.reconcile.by.${lastRun.started_by}`), period: lastRun.period, found: lastRun.found }) }}
    </p>
    <p v-else-if="!queue.isPending.value" class="text-muted-foreground text-xs">{{ t("billing.reconcile.neverRun") }}</p>

    <ul class="flex flex-wrap gap-2" :aria-label="t('billing.reconcile.counts')">
      <li v-for="s in STATUSES" :key="s"><Badge :variant="s === 'open' && counts[s] ? 'destructive' : 'secondary'">{{ label(t, te, "discrepancyStatus", s) }}: {{ counts[s] ?? 0 }}</Badge></li>
    </ul>

    <ErrorText :error="queue.error.value" />
    <ErrorText :error="error" />
    <div class="-mx-3 border-t pt-3">
      <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
        <FilterSelect v-model="viewDraft" :label="t('billing.reconcile.show')" :options="viewOptions" />
        <FilterSelect v-model="kindFilter" multiple :label="t('billing.payments.kind')" :options="kindOptions" />
        <FilterSelect v-if="view === 'all'" v-model="statusFilter" multiple :label="t('billing.payments.status')" :options="statusOptions" />
        <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      </DataToolbar>
      <BulkBar v-bind="bulk.bar.value" @clear="table.clearSelection()" @select-all="bulk.selectAll()">
        <Button type="button" size="sm" variant="outline" :disabled="bulk.busy.value || table.selectedRows.value.filter(actionable).length === 0" @click="open('resolve', table.selectedRows.value.filter(actionable), true)">
          {{ t("billing.reconcile.resolveSelected") }}
        </Button>
      </BulkBar>
      <p v-if="bulk.note.value" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ bulk.note.value }}</p>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
            <TableHead>{{ t("billing.payments.invoice") }}</TableHead>
            <TableHead>{{ t("billing.payments.kind") }}</TableHead>
            <TableHead>{{ t("billing.payments.status") }}</TableHead>
            <TableHead>{{ t("billing.payments.provider") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.payments.billed") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.payments.ledgerAmount") }}</TableHead>
            <TableHead>{{ t("billing.reconcile.owner") }}</TableHead>
            <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(d, i) in table.items.value" :key="d.id" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" @click="table.open(i)">
            <TableCell @click.stop><RowCheck :label="`${t('common.bulk.selectRow')} ${d.number || d.id}`" :checked="table.isTicked(d)" @change="(on) => table.tick(d, on)" /></TableCell>
            <TableCell @click.stop>
              <PeekInvoice v-if="d.invoice_id" :id="d.invoice_id" :tenant-id="tenantId" class="mono font-medium hover:underline">{{ d.number || d.invoice_id.slice(0, 8) }}</PeekInvoice>
            </TableCell>
            <TableCell>{{ label(t, te, "discrepancy", d.kind) }}</TableCell>
            <TableCell><StatusPill :tone="statusTone(d.status)">{{ label(t, te, "discrepancyStatus", d.status) }}</StatusPill></TableCell>
            <TableCell>{{ label(t, te, "provider", d.provider) }} <span class="mono text-xs">{{ d.provider_ref }}</span></TableCell>
            <TableCell class="text-end tabular-nums">{{ money(d.billed_minor, d.currency) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ money(d.ledger_minor, d.currency) }}</TableCell>
            <TableCell>{{ d.owner }}</TableCell>
            <TableCell @click.stop><RowActions :actions="[...rowActions(d), ...viewRow(i)]" /></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableEmpty v-if="!queue.isPending.value && rows.length === 0 && !queue.error.value">{{ t(view === 'review' ? 'billing.reconcile.nothingToReview' : 'billing.reconcile.nothing') }}</TableEmpty>
      <TableEmpty v-else-if="table.total.value === 0 && table.active.value">{{ t("common.noMatches") }}</TableEmpty>
      <DataPagination v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
      <p v-if="more" class="text-muted-foreground px-4 text-xs" data-testid="queue-more">{{ t("billing.reconcile.more", { n: rows.length }) }}</p>
    </div>
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current ? (current.number || String(current.id)) : ''" :description="current ? label(t, te, 'discrepancy', current.kind) : ''">
    <template v-if="current">
      <div class="mb-4 flex items-center gap-2"><StatusPill :tone="statusTone(current.status)">{{ label(t, te, "discrepancyStatus", current.status) }}</StatusPill></div>
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm" data-testid="discrepancy-detail">
        <template v-for="r in detailRows" :key="r.label"><dt class="text-muted-foreground">{{ r.label }}</dt><dd class="text-end">{{ r.value }}</dd></template>
      </dl>
      <p v-if="current.detail" class="text-muted-foreground mt-3 text-sm">{{ current.detail }}</p>
      <div v-if="actionable(current)" class="mt-4 flex flex-wrap items-end gap-2">
        <FormField :label="t('billing.reconcile.owner')" for="rq-owner"><Input id="rq-owner" v-model="owner" class="w-48" maxlength="120" /></FormField>
        <Button type="button" variant="outline" size="sm" :disabled="actions.assign.isPending.value" @click="actions.assign.mutate({ id: current.id, owner })">{{ t("billing.reconcile.assign") }}</Button>
        <Button type="button" variant="outline" size="sm" @click="open('explain', [current])">{{ t("billing.reconcile.explain") }}</Button>
        <Button type="button" size="sm" @click="open('resolve', [current])">{{ t("billing.reconcile.resolve") }}</Button>
      </div>
      <h3 class="mt-6 text-sm font-semibold">{{ t("billing.reconcile.history") }}</h3>
      <ol class="mt-2 grid gap-2 text-sm" data-testid="discrepancy-events">
        <li v-for="e in events" :key="e.id" class="border-s ps-3">
          <span class="font-medium">{{ label(t, te, "discrepancyEvent", e.action) }}</span>
          <span class="text-muted-foreground"> · {{ e.actor }} · {{ when(e.created_at) }}</span>
          <span v-if="e.resolution" class="text-muted-foreground"> · {{ label(t, te, "resolution", e.resolution) }}</span>
          <p v-if="e.note" class="text-muted-foreground">{{ e.note }}</p>
        </li>
      </ol>
    </template>
  </DetailSheet>

  <Dialog :open="dialog !== null" @update:open="(o) => !o && (dialog = null)">
    <DialogContent class="sm:max-w-lg">
      <form class="grid gap-4" @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>{{ dialog?.mode === "explain" ? t("billing.reconcile.explain") : t("billing.reconcile.resolve") }}{{ dialog && dialog.rows.length > 1 ? ` (${dialog.rows.length})` : "" }}</DialogTitle>
          <DialogDescription>{{ dialog?.mode === "explain" ? t("billing.reconcile.explainHint") : t("billing.reconcile.resolveHint") }}</DialogDescription>
        </DialogHeader>
        <FormField v-if="dialog?.mode === 'resolve'" :label="t('billing.reconcile.resolution')" for="rq-resolution">
          <NativeSelect id="rq-resolution" v-model="form.resolution">
            <option v-for="r in choices" :key="r" :value="r">{{ label(t, te, "resolution", r) }}</option>
          </NativeSelect>
        </FormField>
        <template v-if="fact">
          <p class="text-muted-foreground text-sm">{{ t("billing.reconcile.factHint") }}</p>
          <FormField :label="t('billing.payments.provider')" for="rq-provider"><Input id="rq-provider" v-model="form.provider" placeholder="stripe" required /></FormField>
          <FormField :label="t('billing.payments.reference')" for="rq-ref"><Input id="rq-ref" v-model="form.ref" required /></FormField>
        </template>
        <FormField :label="t('billing.reconcile.note')" for="rq-note" :hint="t('billing.reconcile.noteHint')">
          <Textarea id="rq-note" v-model="form.note" rows="3" maxlength="1000" required />
        </FormField>
        <FormField :label="t('billing.reconcile.owner')" for="rq-dialog-owner"><Input id="rq-dialog-owner" v-model="form.owner" maxlength="120" /></FormField>
        <DialogFooter>
          <Button type="button" variant="outline" @click="dialog = null">{{ t("common.cancel") }}</Button>
          <Button type="submit" :disabled="!valid">{{ dialog?.mode === "explain" ? t("billing.reconcile.explain") : t("billing.reconcile.resolve") }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
