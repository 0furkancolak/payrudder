<script setup lang="ts">
import { computed } from "vue";
import { qrShape } from "@/shared/lib/qr";

/**
 * A QR code of `value`, drawn here (shared/lib/qr): the text is never sent anywhere. It is always black on white with the standard four-module border,
 * whatever the theme, because a scanner reads dark modules on a light ground and a dark page would invert it.
 */
const props = defineProps<{ value: string; label: string }>();
const shape = computed(() => qrShape(props.value));
</script>

<template>
  <svg :viewBox="`0 0 ${shape.size} ${shape.size}`" role="img" :aria-label="label" shape-rendering="crispEdges" class="size-52 max-w-full rounded-xl bg-white" data-testid="qr-code">
    <path :d="shape.path" fill="#000" />
  </svg>
</template>
