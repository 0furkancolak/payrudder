<script setup lang="ts">
import type { App } from "./api";
import GoLiveChecklist from "./GoLiveChecklist.vue";

/** An app whose live environment is not active, on the home's live view: its name and its checklist, where the other cards would show figures. */
defineProps<{ app: App }>();
</script>

<template>
  <div class="flex flex-col gap-2" :data-testid="`golive-card-${app.slug}`">
    <div class="px-2">
      <h3 class="truncate text-lg font-semibold tracking-tight">{{ app.name }}</h3>
      <p class="text-muted-foreground mono truncate text-xs">{{ app.slug }}</p>
    </div>
    <GoLiveChecklist :app-id="app.id ?? ''" compact />
  </div>
</template>
