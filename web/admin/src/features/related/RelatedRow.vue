<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { recordLink } from "./link";
import { peek, PEEK_KINDS, type PeekKind } from "./peek";

/**
 * One related record as a row that opens its sheet: whatever is in the slot, on the tinted row every related list uses. A record with a sheet of its own
 * (see peek.ts) opens over the sheet it was named in; the others take the reader to their list, which opens the record.
 */
const props = defineProps<{ tenantId: string; kind: Parameters<typeof recordLink>[0]; id: string }>();
const link = computed(() => recordLink(props.kind, props.id));
const peekable = computed(() => (PEEK_KINDS as readonly string[]).includes(props.kind));
const cls = "bg-muted/50 hover:bg-muted flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 text-start text-sm";
</script>

<template>
  <li>
    <button v-if="peekable" type="button" :class="cls" :data-testid="`related-${kind}`" @click="peek(kind as PeekKind, tenantId, id)"><slot /></button>
    <Link v-else :to="link.to" :params="{ id: tenantId }" :search="link.search" :class="cls" :data-testid="`related-${kind}`"><slot /></Link>
  </li>
</template>
