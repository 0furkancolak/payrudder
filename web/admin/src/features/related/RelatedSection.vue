<script setup lang="ts">
import { useI18n } from "vue-i18n";

/**
 * One group of the records related to the one a sheet shows: a title and its rows, or a word saying there are none. The `action` slot sits at the end of
 * the title (a "view all" link); `emptyText` replaces the default "none yet".
 */
defineProps<{ title: string; empty?: boolean; emptyText?: string; hideTitle?: boolean }>();
const { t } = useI18n();
</script>

<template>
  <section class="flex flex-col gap-2" :aria-label="title">
    <div v-if="!hideTitle || $slots.action" class="flex min-h-7 items-center justify-between gap-3">
      <h3 :class="hideTitle ? 'sr-only' : 'text-sm font-semibold'">{{ title }}</h3>
      <slot name="action" />
    </div>
    <p v-if="empty" class="text-muted-foreground border-border/80 m-0 rounded-xl border border-dashed px-3 py-4 text-center text-sm" data-testid="related-empty">{{ emptyText ?? t("related.none") }}</p>
    <ul v-else class="m-0 flex list-none flex-col gap-1.5 p-0"><slot /></ul>
  </section>
</template>
