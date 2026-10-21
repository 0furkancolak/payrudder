<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/shared/lib/utils"

const props = defineProps<{
  class?: HTMLAttributes["class"]
}>()
</script>

<template>
  <div
    data-slot="card"
    :class="
      cn(
        'bg-card text-card-foreground shadow-card flex min-w-0 flex-col gap-4 rounded-3xl border border-border/50 p-6',
        props.class,
      )
    "
  >
    <slot />
  </div>
</template>
