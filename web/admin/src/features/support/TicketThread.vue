<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { Badge } from "@/shared/ui/badge";
import { formatDate } from "@/shared/lib/format";
import type { SupportMessage } from "./api";

/** The messages of a ticket, oldest first. The operator's internal notes (only the operator's side receives them) are drawn apart. */
defineProps<{ messages: readonly SupportMessage[] }>();
const { t, locale } = useI18n();
</script>

<template>
  <ol class="flex flex-col gap-3" data-testid="support-thread">
    <li
      v-for="m in messages"
      :key="m.id"
      class="rounded-2xl px-4 py-3 text-sm"
      :class="m.internal ? 'bg-warning/10 border-warning/40 border border-dashed' : m.author_kind === 'operator' ? 'bg-primary/10' : 'bg-muted'"
      :data-testid="`support-message-${m.author_kind}${m.internal ? '-internal' : ''}`"
    >
      <div class="text-muted-foreground mb-1 flex flex-wrap items-center gap-2 text-xs">
        <span class="text-foreground font-medium">{{ m.author_name || t(`support.author.${m.author_kind}`) }}</span>
        <Badge v-if="m.internal" variant="outline">{{ t("support.internalNote") }}</Badge>
        <time :datetime="m.create_time">{{ formatDate(m.create_time, locale, "dateTime") }}</time>
      </div>
      <p class="break-words whitespace-pre-wrap">{{ m.body }}</p>
    </li>
  </ol>
</template>
