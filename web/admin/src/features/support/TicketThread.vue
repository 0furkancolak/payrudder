<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { Badge } from "@/shared/ui/badge";
import { formatDate } from "@/shared/lib/format";
import type { SupportEvent, SupportMessage } from "./api";

/**
 * The messages of a ticket, oldest first, with the changes of its status, priority and assignee between them (the history, as small lines: who changed what, from
 * and to). The operator's internal notes (only the operator's side receives them) are drawn apart.
 */
const props = defineProps<{ messages: readonly SupportMessage[]; events?: readonly SupportEvent[] }>();
const { t, locale } = useI18n();

type Entry = { key: string; at: number; message?: SupportMessage; event?: SupportEvent };
// A message sorts before the change made at the same moment (an answer, then the status it set).
const entries = computed<Entry[]>(() =>
  [
    ...props.messages.map((message): Entry => ({ key: `m${message.id}`, at: Date.parse(message.create_time), message })),
    ...(props.events ?? []).map((event): Entry => ({ key: `e${event.id}`, at: Date.parse(event.create_time), event })),
  ].sort((a, b) => a.at - b.at || (a.message ? -1 : 1)),
);

const field = (kind: string) => (kind === "status" ? t("support.columns.status") : kind === "priority" ? t("support.fields.priority") : t("support.columns.assignee"));
const value = (kind: string, v: string) => (kind === "status" ? t(`support.status.${v}`) : kind === "priority" ? t(`support.priority.${v}`) : v || t("support.unassigned"));
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
      </li>
      <li v-else-if="e.event" class="text-muted-foreground flex flex-wrap items-center gap-x-2 px-4 text-xs" :data-testid="`support-event-${e.event.kind}`">
        <span class="font-medium">{{ e.event.actor_name || t(`support.author.${e.event.actor_kind}`) }}</span>
        <span>{{ field(e.event.kind) }}: {{ value(e.event.kind, e.event.from_value) }} → {{ value(e.event.kind, e.event.to_value) }}</span>
        <time :datetime="e.event.create_time">{{ formatDate(e.event.create_time, locale, "dateTime") }}</time>
      </li>
    </template>
  </ol>
</template>
