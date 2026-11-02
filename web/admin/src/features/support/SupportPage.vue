<script setup lang="ts">
import { useQueryClient } from "@tanstack/vue-query";
import { ArrowUpRight, LifeBuoy, Plus } from "@lucide/vue";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAccount, useApps } from "@/features/product/api";
import { formatDate } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { HeaderAction, PageHeader } from "@/shared/ui/page-header";
import { Input } from "@/shared/ui/input";
import { RowActions } from "@/shared/ui/row-actions";
import { NativeSelect } from "@/shared/ui/native-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { useTableState } from "@/shared/lib/useTableState";
import { SortSelect } from "@/shared/ui/sort-select";
import { newest, oldest, time } from "@/shared/lib/sortDefs";
import { attachFiles, CATEGORIES, PRIORITIES, useMySupportActions, useMyTicket, useMyTicketHits, useMyTickets, type SupportCategory, type SupportLink, type SupportPriority, supportKeys } from "./api";
import AttachFailures from "./AttachFailures.vue";
import AttachPicker from "./AttachPicker.vue";
import TicketBadges from "./TicketBadges.vue";
import TicketLinks from "./TicketLinks.vue";
import TicketRating from "./TicketRating.vue";
import TicketThread from "./TicketThread.vue";

/**
 * Help from Payrudder: the tickets of the organisation (every member sees them), a sheet to open one, and a sheet with a ticket's thread to answer on, mark
 * solved or reopen. The operator answers from the support inbox of the operator console. A closed ticket takes no reply.
 */
const { t, locale } = useI18n();
const tickets = useMyTickets();
const account = useAccount();
const apps = useApps();
const actions = useMySupportActions();

const rows = computed(() => tickets.data.value ?? []);
// The page filters on the subject and number it has; the words of the messages are the server's: a ticket it names passes the filter too.
const typed = ref("");
const hits = useMyTicketHits(typed);
const table = useTableState(rows, {
  url: true, searchFields: (r) => [r.subject, r.number, r.app_name, ...(hits.data.value?.has(r.id) ? [typed.value] : [])],
  sorts: () => [newest(t, (r) => time(r.last_message_time)), oldest(t, (r) => time(r.last_message_time))],
  sheet: { kind: "ticket", id: (r) => r.id, loaded: () => tickets.isSuccess.value },
});
watch(() => table.search.value, (v) => (typed.value = v), { immediate: true });
const current = computed(() => table.row.value);
const thread = useMyTicket(() => current.value?.id ?? "");
const messages = computed(() => thread.data.value?.messages ?? []);
const events = computed(() => thread.data.value?.events ?? []);
const shown = computed(() => thread.data.value?.ticket ?? current.value);
// Reading a ticket clears its "new" mark on the server: the list is read again so the row loses it, and so does the count in the sidebar.
const client = useQueryClient();
watch(
  () => thread.data.value,
  () => {
    if (!current.value?.unread) return;
    void tickets.refetch();
    void client.invalidateQueries({ queryKey: supportKeys.unread });
  },
);

// --- a new ticket
const creating = ref(false);
const subject = ref("");
const category = ref<SupportCategory>("question");
const priority = ref<SupportPriority>("normal");
const appId = ref("");
const body = ref("");
/** The objects the new ticket is about; they belong to the app chosen, so a different app starts the list again. */
const newLinks = ref<SupportLink[]>([]);
watch(appId, () => (newLinks.value = []));
const newFiles = ref<File[]>([]);
// The ticket exists once its message is sent; files that then fail are named here and the sheet stays open until the person has read it.
const openedNumber = ref(0);
const newFailures = ref<Awaited<ReturnType<typeof attachFiles>>>([]);
const appOptions = computed(() => (apps.data.value?.apps ?? []).map((a) => ({ id: a.id ?? "", name: a.name ?? "" })).filter((a) => a.id !== ""));
watch(creating, (open) => {
  if (!open) return;
  subject.value = "";
  body.value = "";
  category.value = "question";
  priority.value = "normal";
  appId.value = "";
  newLinks.value = [];
  newFiles.value = [];
  newFailures.value = [];
  openedNumber.value = 0;
  actions.open.reset();
});
async function submit() {
  const accountId = account.data.value?.id;
  if (!accountId || openedNumber.value > 0) return;
  try {
    const made = await actions.open.mutateAsync({ account_id: accountId, subject: subject.value.trim(), category: category.value, priority: priority.value, body: body.value.trim(), ...(appId.value ? { app_id: appId.value } : {}), ...(newLinks.value.length > 0 ? { links: newLinks.value } : {}) });
    const failed = made.message ? await attachFiles("person", made.ticket.id, made.message.id, newFiles.value) : [];
    if (failed.length === 0) {
      creating.value = false;
      return;
    }
    openedNumber.value = made.ticket.number;
    newFailures.value = failed;
  } catch {
    // The mutation's error is shown under the form.
  }
}

// --- the thread
const reply = ref("");
const replyFiles = ref<File[]>([]);
const replyFailures = ref<Awaited<ReturnType<typeof attachFiles>>>([]);
watch(() => current.value?.id, () => {
  reply.value = "";
  replyFiles.value = [];
  replyFailures.value = [];
  actions.reply.reset();
});
async function send() {
  const id = current.value?.id;
  if (!id || reply.value.trim() === "") return;
  try {
    const made = await actions.reply.mutateAsync({ id, body: reply.value.trim() });
    reply.value = "";
    const sent = replyFiles.value;
    replyFiles.value = [];
    replyFailures.value = made.message && sent.length > 0 ? await attachFiles("person", id, made.message.id, sent) : [];
    // The files are part of the thread: read it again now that they are on the message.
    if (sent.length > 0) await thread.refetch();
  } catch {
    // The mutation's error is shown under the form.
  }
}
const closed = computed(() => shown.value?.status === "closed");
const merged = computed(() => !!shown.value?.merged_into_id);
const solved = computed(() => shown.value?.status === "solved");
</script>

<template>
  <PageHeader :title="t('shell.items.support')" :description="t('support.intro')" />
  <HeaderAction test-id="support-new" :label="t('support.new')" :icon="Plus" @click="creating = true" />
  <ErrorState :error="tickets.error.value" :retrying="tickets.isFetching.value" class="mb-4" @retry="tickets.refetch()" />

  <Card class="p-3">
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" @clear="table.clear()">
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead class="w-24">{{ t("support.columns.number") }}</TableHead>
          <TableHead>{{ t("support.columns.subject") }}</TableHead>
          <TableHead>{{ t("support.columns.status") }}</TableHead>
          <TableHead class="text-end">{{ t("support.columns.updated") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(r, i) in table.items.value" :key="r.id" class="cursor-pointer" :data-testid="`support-row-${r.number}`" @click="table.open(i)">
          <TableCell class="mono text-sm">#{{ r.number }}</TableCell>
          <TableCell>
            <div class="font-medium">{{ r.subject }}</div>
            <div v-if="r.app_name" class="text-muted-foreground text-xs">{{ r.app_name }}</div>
          </TableCell>
          <TableCell><TicketBadges :ticket="r" /></TableCell>
          <TableCell class="text-muted-foreground text-end text-sm">{{ formatDate(r.last_message_time, locale, "dateTime") }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="[{ key: 'view', label: t('common.view'), icon: ArrowUpRight, onSelect: () => table.open(i) }]" :label="`${t('common.view')} #${r.number}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <div v-if="rows.length === 0 && !tickets.isPending.value && !tickets.isError.value" class="text-muted-foreground flex flex-col items-center gap-2 p-8 text-center text-sm" data-testid="support-empty">
      <LifeBuoy class="size-8" aria-hidden="true" />
      <p>{{ t("support.empty") }}</p>
      <Button variant="outline" @click="creating = true">{{ t("support.new") }}</Button>
    </div>
    <p v-else-if="rows.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="rows.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <Sheet v-model:open="creating" name="new-ticket">
    <SheetContent data-testid="support-new-sheet">
      <SheetHeader>
        <SheetTitle>{{ t("support.new") }}</SheetTitle>
        <SheetDescription>{{ t("support.newIntro") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="support-form" class="grid gap-3" @submit.prevent="submit">
          <label class="grid gap-1 text-sm">{{ t("support.fields.subject") }}
            <Input v-model="subject" required minlength="3" maxlength="200" data-testid="support-subject" />
          </label>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="grid gap-1 text-sm">{{ t("support.fields.category") }}
              <NativeSelect v-model="category" data-testid="support-category">
                <option v-for="c in CATEGORIES" :key="c" :value="c">{{ t(`support.category.${c}`) }}</option>
              </NativeSelect>
            </label>
            <label class="grid gap-1 text-sm">{{ t("support.fields.priority") }}
              <NativeSelect v-model="priority" data-testid="support-priority-select">
                <option v-for="p in PRIORITIES" :key="p" :value="p">{{ t(`support.priority.${p}`) }} · {{ t(`support.answerWithin.${p}`) }}</option>
              </NativeSelect>
            </label>
          </div>
          <p class="text-muted-foreground text-xs">{{ t("support.businessHours") }}</p>
          <label v-if="appOptions.length > 0" class="grid gap-1 text-sm">{{ t("support.fields.app") }}
            <NativeSelect v-model="appId" data-testid="support-app">
              <option value="">{{ t("support.fields.noApp") }}</option>
              <option v-for="a in appOptions" :key="a.id" :value="a.id">{{ a.name }}</option>
            </NativeSelect>
          </label>
          <div v-if="appId" class="grid gap-1 text-sm">
            <span>{{ t("support.links.title") }}</span>
            <TicketLinks :links="newLinks" :app-id="appId" editable @change="newLinks = $event" />
          </div>
          <label class="grid gap-1 text-sm">{{ t("support.fields.body") }}
            <Textarea v-model="body" required rows="7" maxlength="10000" data-testid="support-body" />
          </label>
          <AttachPicker v-model="newFiles" />
        </form>
        <ErrorText :error="actions.open.error.value" />
        <AttachFailures :failures="newFailures" :note="openedNumber > 0 ? t('support.attach.openedButFailed') : undefined" class="mt-3" />
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="creating = false">{{ openedNumber > 0 ? t("common.close") : t("common.cancel") }}</Button>
        <Button v-if="openedNumber === 0" type="submit" form="support-form" :disabled="actions.open.isPending.value || !account.data.value" data-testid="support-submit">{{ t("support.send") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="shown ? `#${shown.number} · ${shown.subject}` : ''" wide>
    <div v-if="shown" class="flex flex-col gap-4" data-testid="support-ticket">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <TicketBadges :ticket="shown" />
        <span class="text-muted-foreground text-xs">{{ t(`support.category.${shown.category}`) }}<template v-if="shown.app_name"> · {{ shown.app_name }}</template></span>
      </div>
      <p v-if="!shown.first_response_time && shown.status === 'open'" class="text-muted-foreground text-xs" data-testid="support-due">
        {{ t("support.dueBy", { time: formatDate(shown.first_response_due, locale, "dateTime") }) }}
      </p>
      <TicketLinks v-if="shown.links.length > 0" :links="shown.links" />
      <TicketThread :messages="messages" :events="events" :ticket-id="shown.id" side="person" />
      <p v-if="merged" class="text-muted-foreground bg-muted rounded-2xl px-4 py-3 text-sm" data-testid="support-merged">{{ t("support.merge.mergedInto", { number: shown.merged_into_number }) }}</p>
      <TicketRating v-else-if="solved || closed" :ticket="shown" />
      <p v-if="closed && !merged" class="text-muted-foreground bg-muted rounded-2xl px-4 py-3 text-sm" data-testid="support-closed">{{ t("support.closedNote") }}</p>
      <form v-else-if="!closed" class="grid gap-2" @submit.prevent="send">
        <Textarea v-model="reply" rows="4" maxlength="10000" :aria-label="t('support.fields.reply')" :placeholder="t('support.fields.reply')" data-testid="support-reply" />
        <AttachPicker v-model="replyFiles" />
        <ErrorText :error="actions.reply.error.value ?? actions.setStatus.error.value" />
        <AttachFailures :failures="replyFailures" />
        <div class="flex flex-wrap items-center justify-between gap-2">
          <Button v-if="!solved" type="button" variant="outline" :disabled="actions.setStatus.isPending.value" data-testid="support-solve" @click="actions.setStatus.mutate({ id: shown.id, status: 'solved' })">{{ t("support.markSolved") }}</Button>
          <Button v-else type="button" variant="outline" :disabled="actions.setStatus.isPending.value" data-testid="support-reopen" @click="actions.setStatus.mutate({ id: shown.id, status: 'open' })">{{ t("support.reopen") }}</Button>
          <Button type="submit" :disabled="actions.reply.isPending.value || reply.trim() === ''" data-testid="support-send-reply">{{ t("support.sendReply") }}</Button>
        </div>
      </form>
    </div>
  </DetailSheet>
</template>
