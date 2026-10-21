<script setup lang="ts">
import { computed } from "vue";

/** A tiny trend line with a soft area under it, in plain SVG. It decorates a number that is already written in words, so it is hidden from assistive technology. */
const props = withDefaults(defineProps<{ values: number[]; width?: number; height?: number }>(), { width: 120, height: 36 });

const pad = 3;
const geometry = computed(() => {
  const v = props.values.length > 1 ? props.values : [0, 0];
  const max = Math.max(...v, 1);
  const stepX = (props.width - pad * 2) / (v.length - 1);
  const pts = v.map((n, i) => [pad + i * stepX, props.height - pad - (n / max) * (props.height - pad * 2)] as const);
  const line = pts.map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  const last = pts[pts.length - 1] ?? [pad, props.height - pad];
  const area = `${line} L${last[0].toFixed(1)} ${props.height} L${pad} ${props.height} Z`;
  return { line, area, last, flat: v.every((n) => n === 0) };
});
</script>

<template>
  <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" class="overflow-visible" aria-hidden="true" focusable="false">
    <path v-if="!geometry.flat" :d="geometry.area" fill="var(--brand)" fill-opacity="0.22" />
    <path :d="geometry.line" fill="none" :stroke="geometry.flat ? 'var(--border)' : 'var(--brand)'" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    <circle v-if="!geometry.flat" :cx="geometry.last[0]" :cy="geometry.last[1]" r="3" fill="var(--primary)" />
  </svg>
</template>
