<script setup lang="ts">
import { ArrowDownRight, ArrowRight, ArrowUpRight, Sparkles } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Sparkline } from "@/shared/ui/sparkline";
import type { Delta } from "./lib";

/**
 * One figure of the dashboard: a label, the number, how it moved since the period before (with the earlier figure as a tooltip), and a line of detail.
 * `good` says which way is good news: revenue rising is, churn rising is not. The arrow and the sign carry the direction; the colour only the verdict.
 */
const props = withDefaults(defineProps<{ label: string; testid: string; delta?: Delta | null; good?: "up" | "down"; previous?: string; spark?: number[] }>(), { good: "up", delta: null, previous: undefined, spark: undefined });
const { t } = useI18n();
const fmt = useFormat();

const verdict = computed<"good" | "bad" | "neutral">(() => {
  const d = props.delta;
  if (!d || d.direction === "none" || d.direction === "flat") return "neutral";
  if (d.direction === "new") return props.good === "up" ? "good" : "bad";
  return d.direction === props.good ? "good" : "bad";
});
const percent = computed(() => {
  const p = props.delta?.percent;
  return p === null || p === undefined ? "" : `${p > 0 ? "+" : ""}${fmt.percent(p)}`;
});
const word = computed(() => {
  const d = props.delta;
  if (!d || d.direction === "none") return "";
  if (d.direction === "new") return t("dash.delta.new");
  return percent.value;
});
const sr = computed(() => {
  const d = props.delta;
  if (!d || d.direction === "none") return "";
  if (d.direction === "new") return t("dash.delta.srNew");
  if (d.direction === "flat") return t("dash.delta.srFlat");
  return t(d.direction === "up" ? "dash.delta.srUp" : "dash.delta.srDown", { percent: percent.value.replace(/^[+-]/, "") });
});
</script>

<template>
  <!-- The header has a fixed height and the figure follows it, so the values of cards with and without a badge or a spark line start at the same height. -->
  <article class="surface flex min-h-36 flex-col gap-3 p-5" :data-testid="`kpi-${testid}`" :aria-label="label">
    <div class="flex min-h-6 items-start justify-between gap-2">
      <p class="text-muted-foreground text-sm">{{ label }}</p>
      <span
        v-if="word"
        :title="previous ? t('dash.delta.before', { value: previous }) : undefined"
        :class="[
          'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
          verdict === 'good' ? 'bg-success-soft text-success' : verdict === 'bad' ? 'bg-destructive-soft text-destructive' : 'bg-muted text-muted-foreground',
        ]"
        :data-testid="`kpi-${testid}-delta`"
      >
        <Sparkles v-if="delta?.direction === 'new'" class="size-3" aria-hidden="true" />
        <ArrowUpRight v-else-if="delta?.direction === 'up'" class="size-3" aria-hidden="true" />
        <ArrowDownRight v-else-if="delta?.direction === 'down'" class="size-3" aria-hidden="true" />
        <ArrowRight v-else class="size-3" aria-hidden="true" />
        <span aria-hidden="true">{{ word }}</span>
        <span class="sr-only">{{ sr }}</span>
      </span>
    </div>
    <div class="flex flex-1 flex-col gap-2">
      <div class="min-w-0">
        <p class="text-2xl font-semibold tracking-tight tabular-nums [overflow-wrap:anywhere] md:text-[1.6rem]" :data-testid="`kpi-${testid}-value`"><slot name="value" /></p>
        <p class="text-muted-foreground mt-0.5 text-xs [overflow-wrap:anywhere]"><slot name="sub" /></p>
      </div>
      <Sparkline v-if="spark && spark.length > 1" :values="spark" :width="150" :height="30" class="mt-auto max-sm:hidden" />
    </div>
  </article>
</template>
