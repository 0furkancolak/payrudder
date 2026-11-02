<script setup lang="ts">
import { Paperclip } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { Badge } from "@/shared/ui/badge";
import { ErrorText } from "@/shared/ui/error-text";
import { formatDate } from "@/shared/lib/format";
import { attachmentLink, type Side, type SupportAttachment, type SupportEvent, type SupportMessage } from "./api";

/**
 * The messages of a ticket, oldest first, with the changes of its status, priority, assignee and tags between them (the history, as small lines: who changed what, from
 * and to). The operator's internal notes (only the operator's side receives them) are drawn apart. A message's files are links that ask the help desk for a
 * short-lived address and open it; `side` says which route to ask (the person's or the operator's).
 */
const props = defineProps<{ messages: readonly SupportMessage[]; events?: readonly SupportEvent[]; ticketId?: string; side?: Side }>();
const { t, locale } = useI18n();

const failed = ref<unknown>();
async function download(a: SupportAttachment) {
  failed.value = undefined;
  try {
    window.open(await attachmentLink(props.side ?? "person", props.ticketId ?? "", a.id), "_blank", "noopener");
  } catch (e) {
    failed.value = e;
  }
}
const size = (n: number) => (n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

type Entry = { key: string; at: number; message?: SupportMessage; event?: SupportEvent };
// A message sorts before the change made at the same moment (an answer, then the status it set).
const entries = computed<Entry[]>(() =>
  [
    ...props.messages.map((message): Entry => ({ key: `m${message.id}`, at: Date.parse(message.create_time), message })),
    ...(props.events ?? []).map((event): Entry => ({ key: `e${event.id}`, at: Date.parse(event.create_time), event })),
  ].sort((a, b) => a.at - b.at || (a.message ? -1 : 1)),
);

const field = (kind: string) =>
  kind === "status" ? t("support.columns.status") : kind === "priority" ? t("support.fields.priority") : kind === "tags" ? t("support.tags.title") : t("support.columns.assignee");
const value = (kind: string, v: string) =>
  kind === "status" ? t(`support.status.${v}`) : kind === "priority" ? t(`support.priority.${v}`) : kind === "tags" ? v.split(",").join(", ") || "—" : v || t("support.unassigned");
</script>

<template>
  <ol class="flex flex-col gap-3" data-testid="support-thread">
    <template v-for="e in entries" :key="e.key">
      <li
        v-if="e.message"
        class="rounded-2xl px-4 py-3 text-sm"
        :class="e.message.internal ? 'bg-warning/10 border-warning/40 border border-dashed' : e.message.author_kind === 'operator' ? 'bg-primary/10' : 'bg-muted'"
        :data-testid="`support-message-${e.message.author_kind}${e.message.internal ? '-internal' : ''}`"
      >
        <div class="text-muted-foreground mb-1 flex flex-wrap items-center gap-2 text-xs">
          <span class="text-foreground font-medium">{{ e.message.author_name || t(`support.author.${e.message.author_kind}`) }}</span>
          <Badge v-if="e.message.internal" variant="outline">{{ t("support.internalNote") }}</Badge>
          <time :datetime="e.message.create_time">{{ formatDate(e.message.create_time, locale, "dateTime") }}</time>
        </div>
        <p class="break-words whitespace-pre-wrap">{{ e.message.body }}</p>
        <ul v-if="e.message.attachments?.length" class="mt-2 flex flex-wrap gap-2" data-testid="support-attachments">
          <li v-for="a in e.message.attachments" :key="a.id">
            <button type="button" class="bg-background hover:bg-accent flex items-center gap-1 rounded-full border px-3 py-1 text-xs" :aria-label="t('support.attach.download', { name: a.filename })" data-testid="support-attachment" @click="download(a)">
              <Paperclip class="size-3" aria-hidden="true" /><span class="max-w-48 truncate">{{ a.filename }}</span><span class="text-muted-foreground">{{ size(a.size_bytes) }}</span>
            </button>
          </li>
        </ul>
      </li>
      <li v-else-if="e.event" class="text-muted-foreground flex flex-wrap items-center gap-x-2 px-4 text-xs" :data-testid="`support-event-${e.event.kind}`">
        <span class="font-medium">{{ e.event.actor_name || t(`support.author.${e.event.actor_kind}`) }}</span>
        <span v-if="e.event.kind === 'merge'">{{ e.event.from_value ? t("support.merge.joined", { number: e.event.from_value }) : t("support.merge.joinedOther", { number: e.event.to_value }) }}</span>
        <span v-else>{{ field(e.event.kind) }}: {{ value(e.event.kind, e.event.from_value) }} <span class="inline-block rtl:rotate-180">→</span> {{ value(e.event.kind, e.event.to_value) }}</span>
        <time :datetime="e.event.create_time">{{ formatDate(e.event.create_time, locale, "dateTime") }}</time>
      </li>
    </template>
    <li v-if="failed"><ErrorText :error="failed" /></li>
  </ol>
</template>
