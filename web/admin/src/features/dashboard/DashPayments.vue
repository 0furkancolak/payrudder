<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { Donut } from "@/shared/ui/donut";
import DashSection from "./DashSection.vue";
import type { DashCurrency, Dashboard } from "./lib";

/** How the payments of the period ended: a ring of paid, processing, failed and refunded, with the count of each. */
const props = defineProps<{ data: Dashboard; block: DashCurrency }>();
const { t } = useI18n();
const fmt = useFormat();

const pay = computed(() => props.block.payments);
const attempts = computed(() => (pay.value?.paid ?? 0) + (pay.value?.failed ?? 0) + (pay.value?.processing ?? 0));
/** The segments in the order of the legend; the colours are the chart tokens of the stylesheet. */
const segments = computed(() => [
  { key: "paid", value: pay.value?.paid ?? 0, color: "var(--brand)" },
  { key: "processing", value: pay.value?.processing ?? 0, color: "var(--chart-processing)" },
  { key: "failed", value: pay.value?.failed ?? 0, color: "var(--chart-failed)" },
  { key: "refunded", value: pay.value?.refunds ?? 0, color: "var(--chart-refunded)" },
]);
</script>

<template>
  <DashSection :title="t('dash.payments.title')" testid="payments-card" :down="data.unavailable?.includes('payments')" tall>
    <div class="flex flex-wrap items-center justify-center gap-6 sm:flex-nowrap xl:flex-wrap 2xl:flex-nowrap">
      <Donut :segments="segments" :label="t('dash.payments.label', { n: attempts })">
        <span class="text-3xl font-semibold tabular-nums">{{ fmt.count(attempts) }}</span>
        <span class="text-muted-foreground text-xs">{{ t("dash.payments.attempts") }}</span>
      </Donut>
      <ul class="flex flex-col gap-2.5 text-sm">
        <li v-for="s in segments" :key="s.key" class="flex items-center gap-2.5">
          <span class="size-3 shrink-0 rounded-full" :style="{ background: s.color }" aria-hidden="true" />
          <span class="flex-1">{{ t(`product.status.${s.key}`) }}</span>
          <span class="font-medium tabular-nums">{{ fmt.count(s.value) }}</span>
        </li>
      </ul>
    </div>
    <p class="text-muted-foreground text-xs">{{ t("dash.payments.note") }}</p>
    <p v-if="data.payments_truncated" class="bg-warning-soft text-warning rounded-xl px-3 py-2 text-xs" role="note">{{ t("dash.payments.truncated") }}</p>
  </DashSection>
</template>
