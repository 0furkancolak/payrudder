<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { ChevronDown } from "@lucide/vue"
import { cn } from "@/shared/lib/utils"

defineOptions({ inheritAttrs: false })
const props = defineProps<{ class?: HTMLAttributes["class"] }>()
const model = defineModel<string | number>()
</script>

<template>
  <span class="relative inline-flex">
    <select
      v-model="model"
      v-bind="$attrs"
      data-slot="native-select"
      :class="cn('border-input bg-card h-10 w-full appearance-none rounded-lg border ps-4 pe-9 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50', props.class)"
    >
      <slot />
    </select>
    <ChevronDown class="text-muted-foreground pointer-events-none absolute end-3 top-1/2 size-4 -translate-y-1/2" />
  </span>
</template>
