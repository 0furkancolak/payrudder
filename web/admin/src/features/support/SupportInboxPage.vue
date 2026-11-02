<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { formatDate } from "@/shared/lib/format";
import { useTableState } from "@/shared/lib/useTableState";
import { SortSelect } from "@/shared/ui/sort-select";
import { newest, oldest, time } from "@/shared/lib/sortDefs";
import type { SortDef } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DataPagination } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { PageHeader } from "@/shared/ui/page-header";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { useBulk } from "@/shared/lib/bulk";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { PRIORITIES, STATUSES, useCannedReplies, useOperatorSupportActions, useOperatorTicket, useQueue, useSupportOperators, type QueueFilter, type SupportPriority, type SupportStatus } from "./api";
import CannedRepliesDialog from "./CannedRepliesDialog.vue";
import SupportReportCard from "./SupportReportCard.vue";
import TicketBadges from "./TicketBadges.vue";
import TicketThread from "./TicketThread.vue";

/**
 * The operator's support inbox: every ticket of every account, filtered by status, priority, assignee or "late" (the first answer is overdue), newest activity
 * first. A ticket opens in a sheet with its whole thread, the operator's internal notes included, and the controls for status, priority and assignee. An
 * answer is public unless it is ticked as an internal note.
 */
const { t, locale } = useI18n();
const filter = reactive<QueueFilter>({ status: "active", priority: "", assignee: "", search: "", late: false, tag: "" });
const queue = useQueue(() => ({ ...filter }));
const actions = useOperatorSupportActions();
const operators = useSupportOperators();
const canned = useCannedReplies();
const managing = ref(false);
// The filters that are a choice wait for Apply (FilterSelect); the two text boxes are searches and apply when left or on Enter.
const statusOptions = computed(() => [
  { value: "active", label: t("support.status.active") },
  ...STATUSES.map((s) => ({ value: s, label: t(`support.status.${s}`) })),
  { value: "", label: t("support.filters.any") },
]);
const priorityOptions = computed(() => [{ value: "", label: t("support.filters.any") }, ...PRIORITIES.map((p) => ({ value: p, label: t(`support.priority.${p}`) }))]);
const lateOptions = computed(() => [{ value: "", label: t("support.filters.any") }, { value: "late", label: t("support.filters.late") }]);
const lateFilter = computed({
  get: () => (filter.late ? "late" : ""),
  set: (v: string) => (filter.late = v === "late"),
});

const rows = computed(() => queue.data.value ?? []);
const rank: Record<string, number> = { low: 0, normal: 1, high: 2, urgent: 3 };
const sorts = (): SortDef<(typeof rows.value)[number]>[] => [
  newest(t, (r) => time(r.last_message_time)),
  oldest(t, (r) => time(r.last_message_time)),
  { key: "priority_desc", label: t("support.sortPriority"), by: (r) => rank[r.priority] ?? 0, dir: "desc" },
  { key: "due_asc", label: t("support.sortDue"), by: (r) => time(r.first_response_due), dir: "asc" },
];
const table = useTableState(rows, { url: true, searchFields: () => [], sorts, sheet: { kind: "ticket", id: (r) => r.id, loaded: () => queue.isSuccess.value } });
const current = computed(() => table.row.value);
const thread = useOperatorTicket(() => current.value?.id ?? "");
const messages = computed(() => thread.data.value?.messages ?? []);
const events = computed(() => thread.data.value?.events ?? []);
const shown = computed(() => thread.data.value?.ticket ?? current.value);

const reply = ref("");
const newTag = ref("");
/** The people a ticket can be assigned to; one that left the list but still holds a ticket stays choosable for that ticket, so the select shows the truth. */
const assignable = computed(() => {
  const list = (operators.data.value ?? []).map((o) => ({ value: o.email, label: o.name ? `${o.name} (${o.email})` : o.email }));
  const have = shown.value?.assignee;
  return have && !list.some((o) => o.value === have) ? [...list, { value: have, label: have }] : list;
});
const internal = ref(false);
watch(
  () => shown.value?.id,
  () => {
    newTag.value = "";
    reply.value = "";
    internal.value = false;
    actions.reply.reset();
    actions.update.reset();
  },
  { immediate: true },
);
// --- bulk: the same change to every ticked ticket, one call each; the ones that failed stay ticked and are named
const bulk = useBulk(table, { name: (row) => `#${row.number}` });
const bulkStatus = ref("");
const bulkPriority = ref("");
const bulkAssignee = ref("");
const operatorOptions = computed(() => (operators.data.value ?? []).map((o) => ({ value: o.email, label: o.name ? `${o.name} (${o.email})` : o.email })));
async function applyBulk() {
  const patch: { status?: SupportStatus; priority?: SupportPriority; assignee?: string } = {};
  if (bulkStatus.value) patch.status = bulkStatus.value as SupportStatus;
  if (bulkPriority.value) patch.priority = bulkPriority.value as SupportPriority;
  if (bulkAssignee.value) patch.assignee = bulkAssignee.value;
  if (Object.keys(patch).length === 0) return;
  // A closed ticket takes no more answers, so closing asks first (with the count, as every bulk question does).
  const closing = patch.status === "closed" ? (rows: readonly unknown[]) => ({ title: t("support.confirmCloseMany", { n: rows.length }, rows.length), body: t("support.confirmCloseBody") }) : undefined;
  await bulk.run((row) => actions.update.mutateAsync({ id: row.id, ...patch }), { confirm: closing });
  bulkStatus.value = bulkPriority.value = bulkAssignee.value = "";
}

/** Closing is the one status the person cannot take back: the select asks first and goes back to the ticket's status when the answer is no. */
async function setStatus(el: HTMLSelectElement) {
  const status = el.value as SupportStatus;
  if (status === "closed" && !(await askConfirm({ title: t("support.confirmCloseTitle", { number: shown.value?.number ?? "" }), body: t("support.confirmCloseBody") }))) {
    el.value = shown.value?.status ?? "";
    return;
  }
  change({ status });
}
function change(patch: { status?: SupportStatus; priority?: SupportPriority; assignee?: string; tags?: string[] }) {
  const id = shown.value?.id;
  if (id) actions.update.mutate({ id, ...patch });
}
/** Adds the typed tag to the ticket's own (the server lowers and checks it, and says so when it is no tag). */
function addTag() {
  const tag = newTag.value.trim().toLowerCase();
  if (!tag || !shown.value) return;
  newTag.value = "";
  if (!shown.value.tags.includes(tag)) change({ tags: [...shown.value.tags, tag] });
}
const removeTag = (tag: string) => shown.value && change({ tags: shown.value.tags.filter((x) => x !== tag) });
/** Pastes a canned reply at the end of what is written. */
function paste(id: string) {
  const text = canned.data.value?.find((c) => c.id === id)?.body;
  if (text) reply.value = reply.value.trim() === "" ? text : `${reply.value.trimEnd()}\n\n${text}`;
}
function send() {
  const id = shown.value?.id;
  if (!id || reply.value.trim() === "") return;
  actions.reply.mutate({ id, body: reply.value.trim(), internal: internal.value }, { onSuccess: () => ((reply.value = ""), (internal.value = false)) });
}
</script>

<template>
  <PageHeader :title="t('shell.items.supportInbox')" :description="t('support.inboxIntro')" />
  <ErrorState :error="queue.error.value" :retrying="queue.isFetching.value" class="mb-4" @retry="queue.refetch()" />
  <SupportReportCard />

  <Card class="p-3">
    <BulkBar v-bind="bulk.bar.value" @clear="table.clearSelection()" @select-all="bulk.selectAll()">
      <NativeSelect v-model="bulkStatus" :aria-label="t('support.columns.status')" data-testid="bulk-status">
        <option value="">{{ t("support.columns.status") }}</option>
        <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`support.status.${s}`) }}</option>
      </NativeSelect>
      <NativeSelect v-model="bulkPriority" :aria-label="t('support.fields.priority')" data-testid="bulk-priority">
        <option value="">{{ t("support.fields.priority") }}</option>
        <option v-for="p in PRIORITIES" :key="p" :value="p">{{ t(`support.priority.${p}`) }}</option>
      </NativeSelect>
      <NativeSelect v-model="bulkAssignee" :aria-label="t('support.columns.assignee')" data-testid="bulk-assignee">
        <option value="">{{ t("support.columns.assignee") }}</option>
        <option v-for="o in operatorOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
      </NativeSelect>
      <Button type="button" size="sm" :disabled="bulk.busy.value" data-testid="bulk-apply" @click="applyBulk">{{ t("common.applyFilter") }}</Button>
    </BulkBar>
    <p v-if="bulk.note.value" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ bulk.note.value }}</p>
    <div class="flex flex-wrap items-end gap-3 p-2" data-testid="support-filters">
      <FilterSelect v-model="filter.status" :label="t('support.columns.status')" :options="statusOptions" />
      <FilterSelect v-model="filter.priority" :label="t('support.fields.priority')" :options="priorityOptions" />
      <label class="grid gap-1 text-xs">{{ t("support.columns.assignee") }}
        <Input v-model.lazy="filter.assignee" class="w-40" data-testid="filter-assignee" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("support.tags.filter") }}
        <Input v-model.lazy="filter.tag" class="w-32" maxlength="30" data-testid="filter-tag" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("common.search") }}
        <Input v-model.lazy="filter.search" class="w-56" :placeholder="t('support.filters.searchHint')" data-testid="filter-search" />
      </label>
      <SortSelect v-model="table.sort.value" :options="table.sorts.value" />
      <FilterSelect v-model="lateFilter" :label="t('support.filters.late')" :options="lateOptions" />
    </div>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
          <TableHead class="w-24">{{ t("support.columns.number") }}</TableHead>
          <TableHead>{{ t("support.columns.subject") }}</TableHead>
          <TableHead>{{ t("support.columns.account") }}</TableHead>
          <TableHead>{{ t("support.columns.status") }}</TableHead>
          <TableHead>{{ t("support.columns.assignee") }}</TableHead>
          <TableHead class="text-end">{{ t("support.columns.updated") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in table.items.value" :key="r.id" class="cursor-pointer" :data-testid="`inbox-row-${r.number}`" @click="table.open(i)">
          <TableCell><RowCheck :label="t('common.bulk.selectRow')" :checked="table.isTicked(r)" @change="(on) => table.tick(r, on)" /></TableCell>
          <TableCell class="mono text-sm">#{{ r.number }}</TableCell>
          <TableCell class="font-medium">
            {{ r.subject }}
            <span v-if="r.tags.length > 0" class="ms-2 inline-flex flex-wrap gap-1 align-middle"><Badge v-for="tag in r.tags" :key="tag" variant="outline" :data-testid="`row-tag-${tag}`">{{ tag }}</Badge></span>
          </TableCell>
          <TableCell class="text-sm">{{ r.account_name }}<span v-if="r.app_name" class="text-muted-foreground"> · {{ r.app_name }}</span></TableCell>
          <TableCell><TicketBadges :ticket="r" /></TableCell>
          <TableCell class="text-sm">{{ r.assignee || t("support.unassigned") }}</TableCell>
          <TableCell class="text-muted-foreground text-end text-sm">{{ formatDate(r.last_message_time, locale, "dateTime") }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="rows.length === 0 && !queue.isPending.value" class="text-muted-foreground p-6 text-center text-sm" data-testid="inbox-empty">{{ t("support.inboxEmpty") }}</p>
    <DataPagination v-if="rows.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="shown ? `#${shown.number} · ${shown.subject}` : ''" wide>
    <div v-if="shown" class="flex flex-col gap-4" data-testid="inbox-ticket">
      <p class="text-muted-foreground text-xs">{{ shown.opened_by_name }} · {{ shown.account_name }}<template v-if="shown.app_name"> · {{ shown.app_name }}</template> · {{ t(`support.category.${shown.category}`) }}</p>
      <div class="grid gap-3 sm:grid-cols-3">
        <label class="grid gap-1 text-xs">{{ t("support.columns.status") }}
          <NativeSelect :model-value="shown.status" data-testid="ticket-status" @change="setStatus($event.target as HTMLSelectElement)">
            <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`support.status.${s}`) }}</option>
          </NativeSelect>
        </label>
        <label class="grid gap-1 text-xs">{{ t("support.fields.priority") }}
          <NativeSelect :model-value="shown.priority" data-testid="ticket-priority" @change="change({ priority: ($event.target as HTMLSelectElement).value as SupportPriority })">
            <option v-for="p in PRIORITIES" :key="p" :value="p">{{ t(`support.priority.${p}`) }}</option>
          </NativeSelect>
        </label>
        <label class="grid gap-1 text-xs">{{ t("support.columns.assignee") }}
          <NativeSelect :model-value="shown.assignee" data-testid="ticket-assignee" @change="change({ assignee: ($event.target as HTMLSelectElement).value })">
            <option value="">{{ t("support.unassigned") }}</option>
            <option v-for="o in assignable" :key="o.value" :value="o.value">{{ o.label }}</option>
          </NativeSelect>
        </label>
      </div>
      <div class="flex flex-wrap items-center gap-2" data-testid="ticket-tags">
        <span class="text-muted-foreground text-xs">{{ t("support.tags.title") }}</span>
        <Badge v-for="tag in shown.tags" :key="tag" variant="secondary" class="gap-1.5" :data-testid="`tag-${tag}`">
          {{ tag }}
          <button type="button" class="hover:text-destructive -me-1 rounded-full px-1" :aria-label="t('support.tags.remove', { tag })" :data-testid="`tag-remove-${tag}`" @click="removeTag(tag)">×</button>
        </Badge>
        <form class="flex items-center gap-1" @submit.prevent="addTag">
          <Input v-model="newTag" class="h-8 w-28" maxlength="30" :placeholder="t('support.tags.add')" :aria-label="t('support.tags.add')" data-testid="tag-input" />
        </form>
        <span class="text-muted-foreground text-xs">{{ t("support.tags.hint") }}</span>
      </div>
      <p class="text-xs" :class="shown.sla_breached ? 'text-destructive' : 'text-muted-foreground'" data-testid="ticket-sla">
        {{ shown.first_response_time ? t("support.answeredAt", { time: formatDate(shown.first_response_time, locale, "dateTime") }) : t("support.dueBy", { time: formatDate(shown.first_response_due, locale, "dateTime") }) }}
        <TicketBadges :ticket="shown" class="ms-2" />
      </p>
      <TicketThread :messages="messages" :events="events" />
      <form class="grid gap-2" @submit.prevent="send">
        <Textarea v-model="reply" rows="5" maxlength="10000" :aria-label="t('support.fields.reply')" :placeholder="internal ? t('support.fields.note') : t('support.fields.reply')" data-testid="inbox-reply" />
        <ErrorText :error="actions.reply.error.value ?? actions.update.error.value" />
        <div class="flex flex-wrap items-center gap-2">
          <NativeSelect :model-value="''" :aria-label="t('support.canned.insert')" data-testid="inbox-canned" @change="(e: Event) => { paste((e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = ''; }">
            <option value="">{{ t("support.canned.insert") }}</option>
            <option v-for="c in canned.data.value ?? []" :key="c.id" :value="c.id">{{ c.title }}</option>
          </NativeSelect>
          <Button type="button" variant="ghost" size="sm" data-testid="canned-manage" @click="managing = true">{{ t("support.canned.manage") }}</Button>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <label class="flex items-center gap-2 text-sm">
            <Checkbox :model-value="internal" data-testid="inbox-internal" @update:model-value="(v: boolean | 'indeterminate') => (internal = v === true)" />{{ t("support.internalNote") }}
          </label>
          <Button type="submit" :disabled="actions.reply.isPending.value || reply.trim() === ''" data-testid="inbox-send">{{ internal ? t("support.addNote") : t("support.sendReply") }}</Button>
        </div>
      </form>
    </div>
  </DetailSheet>
  <CannedRepliesDialog v-model:open="managing" />
</template>
