<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { recordLink } from "./link";

/** One related record as a row that opens its sheet: whatever is in the slot, on the tinted row every related list uses. */
const props = defineProps<{ tenantId: string; kind: Parameters<typeof recordLink>[0]; id: string }>();
const link = computed(() => recordLink(props.kind, props.id));
</script>

<template>
  <li>
    <Link :to="link.to" :params="{ id: tenantId }" :search="link.search" class="bg-muted/50 hover:bg-muted flex items-center justify-between gap-3 rounded-xl px-3 py-2 text-sm" :data-testid="`related-${kind}`"><slot /></Link>
  </li>
</template>
