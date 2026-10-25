<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { StatusPill } from "@/shared/ui/status-pill";
import DashSection from "./DashSection.vue";
import type { Dashboard } from "./lib";

/** How close the customers are to the limits of their packages, per meter: who is over, who is near, and the heaviest users as bars. */
const props = defineProps<{ data: Dashboard }>();
const { t } = useI18n();
const fmt = useFormat();

const meters = computed(() => props.data.usage?.meters ?? []);
const barColor = (percent: number) => (percent >= 100 ? "bg-destructive/70" : percent >= 80 ? "bg-warning/70" : "bg-brand");
const clamp = (percent: number | undefined) => Math.min(100, percent ?? 0);
</script>

<template>
  <DashSection :title="t('dash.usage.title')" testid="usage" :down="data.unavailable?.includes('usage')" :empty="meters.length === 0" :empty-text="t('dash.usage.empty')">
    <p class="text-muted-foreground text-xs">{{ t("dash.usage.intro", { n: data.usage?.sampled ?? 0, period: data.usage?.period_id ?? "" }) }}</p>
    <ul class="flex flex-col gap-4">
      <li v-for="m in meters" :key="m.meter_code" class="flex flex-col gap-2">
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span class="mono font-medium">{{ m.meter_code }}</span>
          <span class="flex gap-1.5">
            <StatusPill v-if="(m.over ?? 0) > 0" tone="danger">{{ t("dash.usage.over", { n: m.over }) }}</StatusPill>
            <StatusPill v-if="(m.near ?? 0) > 0" tone="warning">{{ t("dash.usage.near", { n: m.near }) }}</StatusPill>
            <StatusPill v-if="(m.over ?? 0) === 0 && (m.near ?? 0) === 0" tone="success">{{ t("dash.usage.ok") }}</StatusPill>
          </span>
        </div>
        <ul class="flex flex-col gap-1.5">
          <li v-for="c in m.top" :key="c.customer_external_id" class="flex items-center gap-3 text-xs">
            <span class="w-24 shrink-0 truncate">{{ c.customer_external_id }}</span>
            <span class="bg-muted h-2 flex-1 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="clamp(c.percent)" aria-valuemin="0" aria-valuemax="100" :aria-label="c.customer_external_id">
              <span :class="['block h-full rounded-full', barColor(c.percent ?? 0)]" :style="{ width: `${clamp(c.percent)}%` }" />
            </span>
            <span class="w-14 shrink-0 text-end tabular-nums">{{ fmt.percent(c.percent ?? 0) }}</span>
          </li>
        </ul>
      </li>
    </ul>
  </DashSection>
</template>
