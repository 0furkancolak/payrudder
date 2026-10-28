<script setup lang="ts">
import { Plus } from "@lucide/vue";
import type { Component } from "vue";
import { Button } from "@/shared/ui/button";

/**
 * A page's main button (new customer, add webhook, ...), drawn in the header of the frame between the breadcrumb and the tour / privacy / environment
 * controls, wherever the page renders it. On a phone it is only the icon (the label stays for screen readers and as the tooltip). Attributes such as
 * `data-testid`, `disabled` and `@click` go to the button itself.
 */
defineOptions({ inheritAttrs: false });
withDefaults(defineProps<{ label: string; icon?: Component }>(), { icon: Plus });
</script>

<template>
  <Teleport to="#header-actions" defer>
    <Button :title="label" v-bind="$attrs" class="max-sm:w-10 max-sm:px-0 max-sm:has-[>svg]:px-0">
      <component :is="icon" class="size-4" aria-hidden="true" />
      <span class="max-sm:sr-only">{{ label }}</span>
    </Button>
  </Teleport>
</template>
