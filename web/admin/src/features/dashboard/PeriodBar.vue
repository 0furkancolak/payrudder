<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { Input } from "@/shared/ui/input";
import { SegmentedControl } from "@/shared/ui/segmented";
import { Switch } from "@/shared/ui/switch";
import { PERIODS, type DashQuery, type Period } from "./lib";

/**
 * The period of the dashboard: 7, 30 or 90 days, the year so far, or two dates of the person's choice, and whether to set it against the period before.
 * The change is emitted whole; the page keeps it in the address.
 */
const props = defineProps<{ query: DashQuery; today: string }>();
const emit = defineEmits<{ "update:query": [DashQuery] }>();
const { t } = useI18n();

const periodOptions = computed(() => PERIODS.map((p) => ({ value: p, label: t(`dash.period.${p}`) })));
const customFrom =computed(() => props.query.from ?? "");
const customTo = computed(() => props.query.to ?? "");
const bad = computed(() => props.query.period === "custom" && customFrom.value !== "" && customTo.value !== "" && customFrom.value > customTo.value);

function pick(p: Period) {
  if (p === "custom") {
    // Starts from the last 30 days so there is something on the page while the dates are being chosen.
    const to = props.today;
    const d = new Date(`${to}T00:00:00Z`);
    d.setUTCDate(d.getUTCDate() - 29);
    emit("update:query", { ...props.query, period: "custom", from: props.query.from ?? d.toISOString().slice(0, 10), to: props.query.to ?? to });
  } else {
    const rest = { ...props.query };
    delete rest.from;
    delete rest.to;
    emit("update:query", { ...rest, period: p });
  }
}

function setDate(which: "from" | "to", value: string) {
  emit("update:query", { ...props.query, period: "custom", from: which === "from" ? value : customFrom.value, to: which === "to" ? value : customTo.value });
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-x-4 gap-y-3" data-testid="period-bar">
    <SegmentedControl :model-value="query.period" :options="periodOptions" :label="t('dash.period.label')" @update:model-value="pick($event as Period)" />
    <div v-if="query.period === 'custom'" class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('dash.period.custom')">
      <Input type="date" class="w-40" :model-value="customFrom" :max="today" :aria-label="t('dash.period.from')" :aria-invalid="bad" data-testid="dash-from" @update:model-value="setDate('from', String($event))" />
      <span class="text-muted-foreground" aria-hidden="true">–</span>
      <Input type="date" class="w-40" :model-value="customTo" :max="today" :aria-label="t('dash.period.to')" :aria-invalid="bad" data-testid="dash-to" @update:model-value="setDate('to', String($event))" />
      <span v-if="bad" class="text-destructive text-sm" role="alert">{{ t("dash.period.badRange") }}</span>
    </div>
    <label class="flex cursor-pointer items-center gap-2.5 text-sm">
      <Switch :model-value="query.compare" :aria-label="t('dash.period.compare')" data-testid="dash-compare" @update:model-value="emit('update:query', { ...query, compare: Boolean($event) })" />
      <span>{{ t("dash.period.compare") }}</span>
    </label>
  </div>
</template>
