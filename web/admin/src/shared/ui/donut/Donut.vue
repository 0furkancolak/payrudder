<script setup lang="ts">
import { computed } from "vue";

/**
 * A ring chart in plain SVG: no chart library. Segments are drawn as arcs of one circle with stroke-dasharray, so the shapes need no path maths and
 * stay crisp at any size. The centre shows a total; the slot is for it. An empty or all-zero ring is a quiet grey track.
 */
const props = withDefaults(defineProps<{ segments: { key: string; value: number; color: string }[]; size?: number; thickness?: number; label: string }>(), {
  size: 168,
  thickness: 18,
});

const radius = computed(() => (props.size - props.thickness) / 2);
const circumference = computed(() => 2 * Math.PI * radius.value);
const total = computed(() => props.segments.reduce((n, s) => n + s.value, 0));
const gap = 3;
const arcs = computed(() => {
  const shown = props.segments.filter((s) => s.value > 0);
  let offset = 0;
  return shown.map((s) => {
    const share = (s.value / total.value) * circumference.value;
    // A single segment needs no gap; several are separated by a small one so neighbours do not touch.
    const length = shown.length > 1 ? Math.max(share - gap, 0.5) : share;
    const arc = { key: s.key, color: s.color, dash: `${length} ${circumference.value - length}`, offset: -offset };
    offset += share;
    return arc;
  });
});
</script>

<template>
  <div class="relative inline-flex items-center justify-center" :style="{ width: `${size}px`, height: `${size}px` }" role="img" :aria-label="label">
    <svg :width="size" :height="size" :viewBox="`0 0 ${size} ${size}`" class="-rotate-90" aria-hidden="true">
      <circle :cx="size / 2" :cy="size / 2" :r="radius" fill="none" stroke="var(--muted)" :stroke-width="thickness" />
      <circle
        v-for="a in arcs"
        :key="a.key"
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        fill="none"
        :stroke="a.color"
        :stroke-width="thickness"
        stroke-linecap="round"
        :stroke-dasharray="a.dash"
        :stroke-dashoffset="a.offset"
      />
    </svg>
    <div class="absolute inset-0 flex flex-col items-center justify-center text-center"><slot /></div>
  </div>
</template>
