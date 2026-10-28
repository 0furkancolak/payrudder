<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { formatDate } from "@/shared/lib/format";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { DataPagination } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { PageHeader } from "@/shared/ui/page-header";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { PRIORITIES, STATUSES, useOperatorSupportActions, useOperatorTicket, useQueue, type QueueFilter, type SupportPriority, type SupportStatus } from "./api";
import TicketBadges from "./TicketBadges.vue";
import TicketThread from "./TicketThread.vue";

/**
 * The operator's support inbox: every ticket of every account, filtered by status, priority, assignee or "late" (the first answer is overdue), newest activity
 * first. A ticket opens in a sheet with its whole thread, the operator's internal notes included, and the controls for status, priority and assignee. An
 * answer is public unless it is ticked as an internal note.
 */
const { t, locale } = useI18n();
const filter = reactive<QueueFilter>({ status: "active", priority: "", assignee: "", search: "", late: false });
const queue = useQueue(() => ({ ...filter }));
const actions = useOperatorSupportActions();

const rows = computed(() => queue.data.value ?? []);
const table = useTableState(rows, { searchFields: () => [], sheet: { kind: "ticket", id: (r) => r.id, loaded: () => queue.isSuccess.value } });
const current = computed(() => table.row.value);
const thread = useOperatorTicket(() => current.value?.id ?? "");
const messages = computed(() => thread.data.value?.messages ?? []);
const shown = computed(() => thread.data.value?.ticket ?? current.value);

const assignee = ref("");
const reply = ref("");
const internal = ref(false);
watch(
  () => shown.value?.id,
  () => {
    assignee.value = shown.value?.assignee ?? "";
    reply.value = "";
    internal.value = false;
    actions.reply.reset();
    actions.update.reset();
  },
  { immediate: true },
);
function change(patch: { status?: SupportStatus; priority?: SupportPriority; assignee?: string }) {
  const id = shown.value?.id;
  if (id) actions.update.mutate({ id, ...patch });
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

  <Card class="p-3">
    <div class="flex flex-wrap items-end gap-3 p-2" data-testid="support-filters">
      <label class="grid gap-1 text-xs">{{ t("support.columns.status") }}
        <NativeSelect v-model="filter.status" data-testid="filter-status">
          <option value="active">{{ t("support.status.active") }}</option>
          <option value="">{{ t("support.filters.any") }}</option>
          <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`support.status.${s}`) }}</option>
        </NativeSelect>
      </label>
      <label class="grid gap-1 text-xs">{{ t("support.fields.priority") }}
        <NativeSelect v-model="filter.priority" data-testid="filter-priority">
          <option value="">{{ t("support.filters.any") }}</option>
          <option v-for="p in PRIORITIES" :key="p" :value="p">{{ t(`support.priority.${p}`) }}</option>
        </NativeSelect>
      </label>
      <label class="grid gap-1 text-xs">{{ t("support.columns.assignee") }}
        <Input v-model.lazy="filter.assignee" class="w-40" data-testid="filter-assignee" />
      </label>
      <label class="grid gap-1 text-xs">{{ t("common.search") }}
        <Input v-model.lazy="filter.search" class="w-56" :placeholder="t('support.filters.searchHint')" data-testid="filter-search" />
      </label>
      <label class="flex items-center gap-2 pb-2 text-sm">
        <Checkbox :model-value="filter.late" data-testid="filter-late" @update:model-value="(v: boolean | 'indeterminate') => (filter.late = v === true)" />{{ t("support.filters.late") }}
      </label>
    </div>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
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
          <TableCell class="mono text-sm">#{{ r.number }}</TableCell>
          <TableCell class="font-medium">{{ r.subject }}</TableCell>
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
          <NativeSelect :model-value="shown.status" data-testid="ticket-status" @change="change({ status: ($event.target as HTMLSelectElement).value as SupportStatus })">
            <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`support.status.${s}`) }}</option>
          </NativeSelect>
        </label>
        <label class="grid gap-1 text-xs">{{ t("support.fields.priority") }}
          <NativeSelect :model-value="shown.priority" data-testid="ticket-priority" @change="change({ priority: ($event.target as HTMLSelectElement).value as SupportPriority })">
            <option v-for="p in PRIORITIES" :key="p" :value="p">{{ t(`support.priority.${p}`) }}</option>
          </NativeSelect>
        </label>
        <label class="grid gap-1 text-xs">{{ t("support.columns.assignee") }}
          <Input v-model="assignee" maxlength="100" data-testid="ticket-assignee" @change="change({ assignee })" />
        </label>
      </div>
      <p class="text-xs" :class="shown.sla_breached ? 'text-destructive' : 'text-muted-foreground'" data-testid="ticket-sla">
        {{ shown.first_response_time ? t("support.answeredAt", { time: formatDate(shown.first_response_time, locale, "dateTime") }) : t("support.dueBy", { time: formatDate(shown.first_response_due, locale, "dateTime") }) }}
        <TicketBadges :ticket="shown" class="ms-2" />
      </p>
      <TicketThread :messages="messages" />
      <form class="grid gap-2" @submit.prevent="send">
        <Textarea v-model="reply" rows="5" maxlength="10000" :aria-label="t('support.fields.reply')" :placeholder="internal ? t('support.fields.note') : t('support.fields.reply')" data-testid="inbox-reply" />
        <ErrorText :error="actions.reply.error.value ?? actions.update.error.value" />
        <div class="flex flex-wrap items-center justify-between gap-2">
          <label class="flex items-center gap-2 text-sm">
            <Checkbox :model-value="internal" data-testid="inbox-internal" @update:model-value="(v: boolean | 'indeterminate') => (internal = v === true)" />{{ t("support.internalNote") }}
          </label>
          <Button type="submit" :disabled="actions.reply.isPending.value || reply.trim() === ''" data-testid="inbox-send">{{ internal ? t("support.addNote") : t("support.sendReply") }}</Button>
        </div>
      </form>
    </div>
  </DetailSheet>
</template>
