<script setup lang="ts">
import { askConfirm } from "@/shared/lib/confirm";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { ErrorText } from "@/shared/ui/error-text";
import { ErrorState } from "@/shared/ui/error-state";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { RowActions } from "@/shared/ui/row-actions";
import { useTableState } from "@/shared/lib/useTableState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useDeadLetters, useRedrive } from "./api";

const { t } = useI18n();
const queue = ref("");
const list = useDeadLetters(queue);
const redrive = useRedrive();
const queues = computed(() => list.data.value?.queues ?? []);
const messages = computed(() => list.data.value?.messages ?? []);
const queuesTable = useTableState(queues, { sheet: { kind: "dl-queue", id: (q) => q.queue }, searchFields: (q) => [q.queue] });
const messagesTable = useTableState(messages, { sheet: { kind: "dl-message", id: (m) => m.message_id }, searchFields: (m) => [m.message_id, m.reason] });
watch(queuesTable.detail, () => {
  queue.value = queuesTable.row.value?.queue ?? "";
  messagesTable.clear();
});
const note = ref<{ queue: string; count: number } | null>(null);

async function run(name: string, count: number) {
  if (!(await askConfirm({ body: t("deadLetters.confirm", { count, queue: name }) }))) return;
  note.value = null;
  const r = await redrive.mutateAsync(name).catch(() => null);
  if (r) note.value = { queue: name, count: Number(r.redriven ?? 0) };
}
</script>

<template>
  <PageHeader :title="t('deadLetters.title')" :description="t('deadLetters.intro', { pattern: '{queue}.dlq' })" />
  <ErrorState :error="list.error.value" :retrying="list.isFetching.value" class="mb-4" @retry="list.refetch()" />
  <ErrorText :error="redrive.error.value" class="mb-3" />
  <p v-if="note" class="text-success bg-success/10 mb-3 rounded-2xl px-4 py-3 text-sm">{{ t("deadLetters.redriven", note) }}</p>

  <Card class="mb-6 p-3">
    <DataToolbar v-model:search="queuesTable.search.value" :active="queuesTable.active.value" @clear="queuesTable.clear()" />
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("deadLetters.columns.queue") }}</TableHead>
          <TableHead class="text-end">{{ t("deadLetters.columns.messages") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("deadLetters.redrive") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(q, i) in queuesTable.items.value" :key="q.queue" class="cursor-pointer" :data-testid="`dlq-${q.queue}`" @click="queuesTable.open(i)">
          <TableCell class="mono text-sm font-medium">{{ q.queue }}.dlq</TableCell>
          <TableCell class="text-end" data-testid="dlq-count">
            <Badge :variant="Number(q.messages ?? 0) > 0 ? 'destructive' : 'secondary'">{{ Number(q.messages ?? 0) }}</Badge>
          </TableCell>
          <TableCell>
            <RowActions :actions="[{ key: 'redrive', label: t('deadLetters.redrive'), disabled: Number(q.messages ?? 0) === 0, onSelect: () => run(q.queue ?? '', Number(q.messages ?? 0)) }]" />
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="queues.length > 0 && queuesTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="queues.length > 0" v-model:page="queuesTable.page.value" v-model:size="queuesTable.size.value" :pages="queuesTable.pages.value" :total="queuesTable.total.value" />
  </Card>

  <DetailSheet v-model:index="queuesTable.detail.value" :total="queuesTable.total.value" :title="t('deadLetters.messagesOf', { queue })" wide>
    <div class="-mx-3">
      <DataToolbar v-model:search="messagesTable.search.value" :active="messagesTable.active.value" @clear="messagesTable.clear()" />
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("deadLetters.messageColumns.id") }}</TableHead>
            <TableHead class="text-end">{{ t("deadLetters.messageColumns.deliveries") }}</TableHead>
            <TableHead>{{ t("deadLetters.messageColumns.reason") }}</TableHead>
            <TableHead class="text-end">{{ t("deadLetters.messageColumns.bytes") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(m, i) in messagesTable.items.value" :key="`${m.message_id}-${i}`">
            <TableCell class="mono text-xs">{{ m.message_id }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ m.delivery_count }}</TableCell>
            <TableCell class="whitespace-normal">{{ m.reason }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ m.body_bytes }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="messages.length > 0 && messagesTable.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
      <p v-else-if="messages.length === 0 && !list.isPending.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("deadLetters.noMessages") }}</p>
      <DataPagination v-if="messages.length > 0" v-model:page="messagesTable.page.value" v-model:size="messagesTable.size.value" :pages="messagesTable.pages.value" :total="messagesTable.total.value" />
    </div>
  </DetailSheet>
</template>
