<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { formatUtcDay } from "@/shared/lib/format";
import { formatCompact, formatMinor } from "@/shared/lib/money";
import { privacyMode } from "@/shared/lib/prefs";
import { delta, geometry, nearestIndex, type ChartPoint } from "./lib";

/**
 * Net revenue per day (or per month for a long period) as an area, with the period before drawn as a dashed line under the same axis, in plain SVG: no chart
 * library. Hovering (or the arrow keys, when the chart has focus) shows the day with both figures and the change. The gridlines sit on round numbers, and in
 * privacy mode the axis, the tooltip and the label say nothing about amounts.
 */
const props = defineProps<{ points: readonly ChartPoint[]; currency: string; granularity: "day" | "month"; compare: boolean }>();
const { t, locale } = useI18n();

const W = 640;
const H = 220;
const g = computed(() => geometry(props.points, W, H));
const active = ref<number | null>(null);
const box = ref<HTMLDivElement | null>(null);

const ticks = computed(() => [0, 1, 2, 3, 4].map((i) => ({ value: (g.value.max * i) / 4, top: (g.value.y((g.value.max * i) / 4) / H) * 100 })));
const date = (iso: string) => formatUtcDay(iso, locale.value, props.granularity === "month" ? "month" : "monthDay");
const labels = computed(() => {
  const p = props.points;
  if (p.length === 0) return [];
  const idx = p.length < 3 ? [0, p.length - 1] : [0, Math.floor((p.length - 1) / 2), p.length - 1];
  return [...new Set(idx)].map((i) => ({ i, text: date(p[i]!.date), left: (g.value.x(i) / W) * 100 }));
});
const total = computed(() => props.points.reduce((n, p) => n + p.value, 0));
const prevTotal = computed(() => props.points.reduce((n, p) => n + (p.prev ?? 0), 0));
const summary = computed(() =>
  props.points.length === 0 ? t("dash.chart.empty") : t("dash.chart.summary", { from: date(props.points[0]!.date), to: date(props.points[props.points.length - 1]!.date), total: formatMinor(total.value, props.currency, locale.value) }),
);

function onMove(e: PointerEvent) {
  const el = box.value;
  if (!el || props.points.length === 0) return;
  const r = el.getBoundingClientRect();
  active.value = nearestIndex(g.value, props.points.length, ((e.clientX - r.left) / r.width) * W);
}
function onKey(e: KeyboardEvent) {
  const n = props.points.length;
  if (n === 0) return;
  const cur = active.value ?? (e.key === "ArrowLeft" ? n : -1);
  if (e.key === "ArrowRight") active.value = Math.min(n - 1, cur + 1);
  else if (e.key === "ArrowLeft") active.value = Math.max(0, cur - 1);
  else if (e.key === "Home") active.value = 0;
  else if (e.key === "End") active.value = n - 1;
  else if (e.key === "Escape") active.value = null;
  else return;
  e.preventDefault();
}
const tip = computed(() => {
  const i = active.value;
  const p = i === null ? undefined : props.points[i];
  if (i === null || !p) return null;
  const d = props.compare && p.prev !== undefined ? delta(p.value, p.prev) : null;
  const left = (g.value.x(i) / W) * 100;
  return { p, left, top: (g.value.y(p.value) / H) * 100, topPrev: p.prev === undefined ? null : (g.value.y(p.prev) / H) * 100, d, flip: left > 62 };
});
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs">
      <span class="flex items-center gap-1.5"><span class="bg-brand inline-block h-1 w-5 rounded-full" aria-hidden="true" />{{ t("dash.chart.current") }}</span>
      <span v-if="compare" class="text-muted-foreground flex items-center gap-1.5"><span class="inline-block h-0 w-5 border-t-2 border-dashed border-current" aria-hidden="true" />{{ t("dash.chart.previous") }}</span>
      <span class="text-muted-foreground ms-auto tabular-nums" data-testid="chart-total">
        {{ t("dash.chart.total") }} {{ formatMinor(total, currency, locale) }}<template v-if="compare && prevTotal > 0"> · {{ t("dash.chart.before") }} {{ formatMinor(prevTotal, currency, locale) }}</template>
      </span>
    </div>
    <div class="relative ps-12">
      <div
        ref="box"
        class="focus-visible:ring-ring/50 relative h-56 w-full rounded-lg outline-none focus-visible:ring-2"
        tabindex="0"
        role="img"
        :aria-label="summary"
        data-testid="revenue-chart"
        @pointermove="onMove"
        @pointerleave="active = null"
        @focus="active = active ?? Math.max(0, points.length - 1)"
        @blur="active = null"
        @keydown="onKey"
      >
        <svg :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" class="absolute inset-0 size-full" aria-hidden="true" focusable="false">
          <line v-for="tk in ticks" :key="tk.top" x1="0" :x2="W" :y1="(tk.top / 100) * H" :y2="(tk.top / 100) * H" stroke="var(--border)" stroke-width="1" vector-effect="non-scaling-stroke" />
          <path v-if="g.area" :d="g.area" fill="var(--brand)" fill-opacity="0.22" />
          <path v-if="g.prevLine" :d="g.prevLine" fill="none" stroke="var(--muted-foreground)" stroke-opacity="0.8" stroke-width="1.5" stroke-dasharray="5 4" vector-effect="non-scaling-stroke" />
          <path v-if="g.line" :d="g.line" fill="none" stroke="var(--primary)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" vector-effect="non-scaling-stroke" />
          <line v-if="tip" :x1="(tip.left / 100) * W" :x2="(tip.left / 100) * W" y1="0" :y2="H" stroke="var(--muted-foreground)" stroke-opacity="0.5" stroke-width="1" vector-effect="non-scaling-stroke" />
        </svg>
        <template v-if="tip">
          <span class="bg-primary pointer-events-none absolute size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-white dark:ring-[var(--card)]" :style="{ left: `${tip.left}%`, top: `${tip.top}%` }" />
          <span v-if="tip.topPrev !== null" class="bg-muted-foreground pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full" :style="{ left: `${tip.left}%`, top: `${tip.topPrev}%` }" />
          <div
            class="bg-popover text-popover-foreground shadow-pop border-border/70 pointer-events-none absolute z-10 w-max max-w-56 rounded-xl border px-3 py-2 text-xs"
            :style="{ left: `${tip.left}%`, top: '4px', transform: tip.flip ? 'translateX(calc(-100% - 10px))' : 'translateX(10px)' }"
            data-testid="chart-tooltip"
            role="status"
          >
            <p class="text-muted-foreground">{{ date(tip.p.date) }}</p>
            <p class="text-sm font-semibold tabular-nums">{{ formatMinor(tip.p.value, currency, locale) }}</p>
            <template v-if="compare && tip.p.prev !== undefined">
              <p class="text-muted-foreground mt-1">{{ date(tip.p.prevDate ?? "") }}</p>
              <p class="tabular-nums">{{ formatMinor(tip.p.prev, currency, locale) }}</p>
            </template>
          </div>
        </template>
      </div>
      <span v-for="tk in ticks" :key="tk.top" class="text-muted-foreground pointer-events-none absolute start-0 w-11 -translate-y-1/2 pe-1 text-end text-[0.65rem] tabular-nums" :style="{ top: `calc(${tk.top / 100} * 14rem)` }">
        {{ privacyMode && tk.value > 0 ? "••" : formatCompact(tk.value, currency, locale) }}
      </span>
      <div class="text-muted-foreground relative mt-1.5 h-4 text-xs">
        <span v-for="(l, k) in labels" :key="l.i" :class="['absolute whitespace-nowrap', k === 0 ? '' : k === labels.length - 1 ? '-translate-x-full' : '-translate-x-1/2']" :style="{ left: `${l.left}%` }">{{ l.text }}</span>
      </div>
    </div>
  </div>
</template>
