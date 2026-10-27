<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { formatUtcDay } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { barHeights, type SeriesPoint } from "./chart";

/** Net income against expenses per day or month, as paired SVG bars. Each pair has a title for hover and for screen readers. */
const props = defineProps<{ points: readonly SeriesPoint[]; currency: string; granularity: "day" | "month" }>();
const { t, locale } = useI18n();

const H = 100;
const slot = 10;
const bars = computed(() => barHeights(props.points));
const width = computed(() => Math.max(1, bars.value.length) * slot);
const label = (bucket: string) => formatUtcDay(bucket, locale.value, props.granularity === "month" ? "month" : "day");
const title = (i: number) => {
  const p = props.points[i];
  return `${label(p?.bucket ?? "")}: ${t("billing.accounting.bars.income")} ${formatMinor(p?.net_income_minor, props.currency, locale.value)}, ${t("billing.accounting.bars.expense")} ${formatMinor(p?.expenses_minor, props.currency, locale.value)}`;
};
</script>

<template>
  <div>
    <svg :viewBox="`0 0 ${width} ${H}`" preserveAspectRatio="none" class="h-40 w-full" role="img" :aria-label="t('billing.accounting.bars.title')">
      <line x1="0" :x2="width" :y1="H" :y2="H" stroke="currentColor" class="text-border" stroke-width="0.4" vector-effect="non-scaling-stroke" />
      <g v-for="(b, i) in bars" :key="b.bucket">
        <title>{{ title(i) }}</title>
        <rect :x="i * slot + 1" :y="H - b.income * H" :width="slot / 2 - 1" :height="b.income * H" fill="var(--chart-income)" />
        <rect :x="i * slot + slot / 2" :y="H - b.expense * H" :width="slot / 2 - 1" :height="b.expense * H" fill="var(--chart-expense)" />
      </g>
    </svg>
    <!-- The first and last date under the bars, and the legend on a line of its own: on a phone the three did not fit on one line and overlapped. -->
    <div class="text-muted-foreground mt-1 flex items-center justify-between gap-2 text-xs">
      <span>{{ points.length ? label(points[0]?.bucket ?? "") : "" }}</span>
      <span>{{ points.length ? label(points[points.length - 1]?.bucket ?? "") : "" }}</span>
    </div>
    <div class="text-muted-foreground mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
      <span class="flex items-center gap-1.5"><span class="inline-block size-2.5 rounded-sm bg-(--chart-income)" aria-hidden="true" />{{ t("billing.accounting.bars.income") }}</span>
      <span class="flex items-center gap-1.5"><span class="inline-block size-2.5 rounded-sm bg-(--chart-expense)" aria-hidden="true" />{{ t("billing.accounting.bars.expense") }}</span>
    </div>
  </div>
</template>
