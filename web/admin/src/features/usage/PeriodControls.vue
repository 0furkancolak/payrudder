<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { Switch } from "@/shared/ui/switch";

/**
 * The period of the usage pages: the days the series are drawn over, whether to set them against the days before, and (said in words, not chosen) the billing
 * period the limits and rankings are counted in, which is the month the range ends in.
 */
defineProps<{ period: string; tooLong?: boolean }>();
const range = defineModel<DateRange>("range", { required: true });
const compare = defineModel<boolean>("compare", { required: true });
const { t } = useI18n();
</script>

<template>
  <DateRangePicker v-model="range" />
  <label class="flex cursor-pointer items-center gap-2.5 text-sm">
    <Switch v-model="compare" :aria-label="t('billing.usage.compare')" data-testid="usage-compare" />
    <span>{{ t("billing.usage.compare") }}</span>
  </label>
  <span class="text-muted-foreground text-xs" data-testid="usage-period" :title="t('billing.usage.periodHint')">{{ t("billing.usage.period", { period }) }}</span>
  <span v-if="tooLong" class="text-warning text-xs" role="status">{{ t("billing.usage.tooLong") }}</span>
</template>
