<script setup lang="ts">
import { useI18n } from "vue-i18n";

/** One group of the records related to the one a sheet shows: a title and its rows, or a word saying there are none. */
defineProps<{ title: string; empty?: boolean }>();
const { t } = useI18n();
</script>

<template>
  <section class="flex flex-col gap-2" :aria-label="title">
    <h3 class="text-sm font-semibold">{{ title }}</h3>
    <p v-if="empty" class="text-muted-foreground m-0 text-sm">{{ t("related.none") }}</p>
    <ul v-else class="m-0 flex list-none flex-col gap-1.5 p-0"><slot /></ul>
  </section>
</template>
