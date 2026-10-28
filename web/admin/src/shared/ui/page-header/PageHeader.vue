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
  <!-- The title and the actions share the first row, also on a phone (where the create button is an icon); the line under the title takes the full width below. -->
  <header :class="cn('grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-1', !flush && 'mb-6')">
    <div class="flex min-w-0 flex-wrap items-center gap-x-3">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl" :data-testid="titleTestId">{{ title }}</h1>
      <slot name="badge" />
    </div>
    <div class="col-start-2 row-start-1 flex items-center gap-2 empty:hidden">
      <slot />
      <HeaderActions />
    </div>
    <div class="col-span-full min-w-0 empty:hidden">
      <slot name="subtitle">
        <p v-if="description" class="text-muted-foreground max-w-2xl text-sm">{{ description }}</p>
      </slot>
    </div>
  </header>
</template>
