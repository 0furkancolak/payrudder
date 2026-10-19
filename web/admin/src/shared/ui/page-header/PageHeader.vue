<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <header class="mb-6 flex flex-wrap items-end justify-between gap-3 px-1">
    <div>
      <h1 class="text-3xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="description" class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ description }}</p>
    </div>
    <div class="flex items-center gap-2"><slot /></div>
  </header>
</template>
