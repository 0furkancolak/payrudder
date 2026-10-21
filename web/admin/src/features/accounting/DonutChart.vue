<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { donutSlices, type CategoryAmount } from "./chart";

/** Expenses by category as a donut of SVG circles, with the legend (name, amount, share) beside it so the colours are never the only information. */
const props = defineProps<{ items: readonly CategoryAmount[]; currency: string }>();
const { t, locale } = useI18n();
const slices = computed(() => donutSlices(props.items));
const percent = (p: number) => new Intl.NumberFormat(locale.value, { style: "percent", maximumFractionDigits: 1 }).format(p / 100);
const amountOf = (category?: string) => props.items.find((i) => i.category === category)?.amount_minor;
</script>

<template>
  <div class="flex flex-wrap items-center gap-6">
    <svg viewBox="0 0 42 42" class="size-36 shrink-0" role="img" :aria-label="t('billing.accounting.donut.title')">
      <circle cx="21" cy="21" r="15.9155" fill="none" stroke="currentColor" class="text-muted" stroke-width="6" />
      <circle
        v-for="s in slices"
        :key="s.category"
        cx="21"
        cy="21"
        r="15.9155"
        fill="none"
        :stroke="s.color"
        stroke-width="6"
        :stroke-dasharray="s.dash"
        :stroke-dashoffset="s.offset"
      />
    </svg>
    <ul class="grid gap-1.5 text-sm">
      <li v-for="s in slices" :key="s.category" class="flex items-center gap-2">
        <span class="inline-block size-3 shrink-0 rounded-sm" :style="{ backgroundColor: s.color }" aria-hidden="true" />
        <span>{{ t(`billing.accounting.category.${s.category}`) }}</span>
        <span class="text-muted-foreground tabular-nums">{{ formatMinor(amountOf(s.category), currency, locale) }} · {{ percent(s.percent) }}</span>
      </li>
    </ul>
  </div>
</template>
