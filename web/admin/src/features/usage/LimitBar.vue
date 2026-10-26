<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { StatusPill } from "@/shared/ui/status-pill";
import { limitState, percentUsed, formatQuantity, type Entitlement } from "./lib";

/** What one customer used of one limit: a bar, the figures and a word for where they stand (the colour is never the only signal). */
const props = defineProps<{ entitlement: Entitlement; unit?: string }>();
const { t, locale } = useI18n();
const state = computed(() => limitState(props.entitlement));
const percent = computed(() => percentUsed(props.entitlement.used ?? "0", props.entitlement.limit));
const width = computed(() => Math.min(100, percent.value ?? 0));
const tone = { over: "danger", near: "warning", ok: "success", unlimited: "neutral" } as const;
const bar = computed(() => (state.value === "over" ? "bg-destructive/70" : state.value === "near" ? "bg-warning/70" : "bg-brand"));
</script>

<template>
  <div class="flex min-w-0 items-center gap-3">
    <div v-if="state !== 'unlimited'" class="flex min-w-24 flex-1 flex-col gap-1">
      <span class="bg-muted h-2 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="width" aria-valuemin="0" aria-valuemax="100">
        <span :class="['block h-full rounded-full', bar]" :style="{ width: `${width}%` }" />
      </span>
      <span class="text-muted-foreground text-xs tabular-nums">{{ formatQuantity(entitlement.used, locale) }} / {{ formatQuantity(entitlement.limit ?? '', locale) }}<template v-if="unit"> {{ unit }}</template> · {{ percent }}%</span>
    </div>
    <span v-else class="text-muted-foreground text-xs tabular-nums">{{ formatQuantity(entitlement.used, locale) }}<template v-if="unit"> {{ unit }}</template></span>
    <StatusPill :tone="tone[state]" :data-state="state">{{ t(`billing.usage.state.${state}`) }}</StatusPill>
  </div>
</template>
