<script setup lang="ts">
import type { HTMLAttributes } from "vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";

/**
 * One card of the dashboard: a title, then either "this part did not answer" (`down`), "nothing yet" (`empty`) or the content. The head slot replaces
 * the plain title; the footer slot is shown whatever the content is (a note under a list).
 */
const props = defineProps<{ title: string; testid?: string; down?: boolean; empty?: boolean; emptyText?: string; tall?: boolean; class?: HTMLAttributes["class"] }>();
const { t } = useI18n();
</script>

<template>
  <section :class="cn('surface flex flex-col gap-4 p-6', props.class)" :aria-label="title" :data-testid="testid">
    <slot name="head"><h2 class="text-lg font-semibold tracking-tight">{{ title }}</h2></slot>
    <p v-if="down" :class="['text-muted-foreground text-center text-sm', tall ? 'py-10' : 'py-6']">{{ t("product.unavailable") }}</p>
    <p v-else-if="empty" class="text-muted-foreground py-6 text-center text-sm">{{ emptyText }}</p>
    <slot v-else />
    <slot name="footer" />
  </section>
</template>
