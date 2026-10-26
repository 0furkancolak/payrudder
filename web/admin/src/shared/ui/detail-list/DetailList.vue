<script setup lang="ts">
/**
 * The fields of one row as a list of label and value pairs, for the body of a DetailSheet. A value left empty shows a dash; `mono` sets references
 * and ids in the monospaced face. A pair with a slot named after its `key` shows that slot instead of the text.
 */
export interface DetailRow {
  key: string;
  label: string;
  value?: string | number | null;
  mono?: boolean;
}
defineProps<{ rows: DetailRow[] }>();
</script>

<template>
  <dl class="grid gap-x-4 gap-y-3 text-sm sm:grid-cols-[10rem_1fr]">
    <template v-for="r in rows" :key="r.key">
      <dt class="text-muted-foreground">{{ r.label }}</dt>
      <dd :class="['min-w-0 break-words', r.mono ? 'mono text-xs' : '']"><slot :name="r.key">{{ r.value === undefined || r.value === null || r.value === "" ? "—" : r.value }}</slot></dd>
    </template>
  </dl>
</template>
