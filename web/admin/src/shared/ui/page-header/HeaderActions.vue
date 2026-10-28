<script setup lang="ts">
import { Button } from "@/shared/ui/button";
import { Plus } from "@lucide/vue";
import { headerActions } from "./headerActions";
</script>

<template>
  <div v-if="headerActions.length" data-testid="header-actions" class="flex shrink-0 items-center gap-2">
    <Button
      v-for="(a, i) in headerActions"
      :key="i"
      :title="a.props.title ?? a.props.label"
      :disabled="a.props.disabled"
      :data-testid="a.props.testId"
      class="max-sm:w-10 max-sm:px-0 max-sm:has-[>svg]:px-0"
      @click="a.click()"
    >
      <component :is="a.props.icon ?? Plus" class="size-4" aria-hidden="true" />
      <span class="max-sm:sr-only">{{ a.props.label }}</span>
    </Button>
  </div>
</template>
