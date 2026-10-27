<script setup lang="ts">
import { computed, type HTMLAttributes } from "vue"
import { ChevronDown } from "@lucide/vue"
import { cn } from "@/shared/lib/utils"

defineOptions({ inheritAttrs: false })
const props = defineProps<{ class?: HTMLAttributes["class"] }>()
const model = defineModel<string | number | undefined>()
// A model that was never set (a filter nobody touched) shows the option whose value is empty, not a blank box.
const shown = computed({ get: () => model.value ?? "", set: (v) => (model.value = v) })
</script>

<template>
  <span class="relative inline-flex">
    <select
      v-model="shown"
      v-bind="$attrs"
      data-slot="native-select"
      :class="cn('field h-10 w-full appearance-none rounded-xl ps-4 pe-9 text-sm disabled:cursor-not-allowed disabled:opacity-50', props.class)"
    >
      <slot />
    </select>
    <ChevronDown class="text-muted-foreground pointer-events-none absolute end-3 top-1/2 size-4 -translate-y-1/2" />
  </span>
</template>
