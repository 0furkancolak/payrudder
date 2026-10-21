<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { barHeights, type SeriesPoint } from "./chart";

/** Net income against expenses per day or month, as paired SVG bars. Each pair has a title for hover and for screen readers. */
const props = defineProps<{ points: readonly SeriesPoint[]; currency: string; granularity: "day" | "month" }>();
const { t, locale } = useI18n();

const H = 100;
const slot = 10;
const bars = computed(() => barHeights(props.points));
const width = computed(() => Math.max(1, bars.value.length) * slot);
const label = (bucket: string) =>
  new Intl.DateTimeFormat(locale.value, props.granularity === "month" ? { month: "short", year: "numeric", timeZone: "UTC" } : { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${bucket}T00:00:00Z`));
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
        <rect :x="i * slot + 1" :y="H - b.income * H" :width="slot / 2 - 1" :height="b.income * H" fill="#059669" />
        <rect :x="i * slot + slot / 2" :y="H - b.expense * H" :width="slot / 2 - 1" :height="b.expense * H" fill="#dc2626" />
      </g>
    </svg>
    <div class="text-muted-foreground mt-1 flex items-center justify-between text-xs">
      <span>{{ points.length ? label(points[0]?.bucket ?? "") : "" }}</span>
      <span class="flex items-center gap-3">
        <span class="flex items-center gap-1"><span class="inline-block size-2.5 rounded-sm bg-[#059669]" aria-hidden="true" />{{ t("billing.accounting.bars.income") }}</span>
        <span class="flex items-center gap-1"><span class="inline-block size-2.5 rounded-sm bg-[#dc2626]" aria-hidden="true" />{{ t("billing.accounting.bars.expense") }}</span>
      </span>
      <span>{{ points.length ? label(points[points.length - 1]?.bucket ?? "") : "" }}</span>
    </div>
  </div>
</template>
