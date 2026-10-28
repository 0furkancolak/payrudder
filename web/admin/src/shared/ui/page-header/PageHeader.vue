<script setup lang="ts">
import { cn } from "@/shared/lib/utils";
import HeaderActions from "./HeaderActions.vue";

/**
 * The head of a page: the title, a line under it (`description`, or the `subtitle` slot for anything richer), a `badge` next to the title (an environment
 * pill, say) and the page's actions in the default slot, then the page's main create button (a HeaderAction, an icon only on a phone). `flush` drops the space below, for a page that lays out its own gaps.
 */
defineProps<{ title: string; description?: string; flush?: boolean; titleTestId?: string }>();
</script>

<template>
  <header :class="cn('flex flex-wrap items-end justify-between gap-3 px-1', !flush && 'mb-6')">
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-3xl font-semibold tracking-tight md:text-4xl" :data-testid="titleTestId">{{ title }}</h1>
        <slot name="badge" />
      </div>
      <slot name="subtitle">
        <p v-if="description" class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ description }}</p>
      </slot>
    </div>
    <div class="flex items-center gap-2 empty:hidden">
      <slot />
      <HeaderActions />
    </div>
  </header>
</template>
