<script setup lang="ts">
import { useI18n } from "vue-i18n";
import PackageSelect from "@/features/packages/PackageSelect.vue";
import type { Package } from "@/features/packages";
import { NativeSelect } from "@/shared/ui/native-select";
import { INTERVALS } from "./lib";
import type { ChangeInput } from "./api";

/**
 * The choices of a change of plan: which package, which interval, and how to prorate (and, with `showTiming`, when). An empty choice keeps what the
 * subscription has. Any change tells the parent (`change`), which drops a quote that no longer matches.
 */
defineProps<{ packages: readonly Package[]; showTiming?: boolean; noneKey: string }>();
const emit = defineEmits<{ change: [] }>();
const { t } = useI18n();
const plan = defineModel<string>("plan", { required: true });
const interval = defineModel<ChangeInput["interval"]>("interval", { required: true });
const timing = defineModel<ChangeInput["timing"]>("timing");
const proration = defineModel<ChangeInput["proration"]>("proration", { required: true });
</script>

<template>
  <PackageSelect v-model="plan" :packages="packages" :label="t('billing.subscriptions.change.plan')" :placeholder="t('billing.subscriptions.change.keepPlan')" @change="emit('change')" />
  <NativeSelect v-model="interval" :aria-label="t('billing.subscriptions.change.interval')" @change="emit('change')">
    <option value="">{{ t("billing.subscriptions.change.keepInterval") }}</option>
    <option v-for="i in INTERVALS" :key="i" :value="i">{{ t(i === "SUBSCRIPTION_INTERVAL_YEAR" ? "billing.subscriptions.yearly" : "billing.subscriptions.monthly") }}</option>
  </NativeSelect>
  <NativeSelect v-if="showTiming" v-model="timing" :aria-label="t('billing.subscriptions.change.when')" @change="emit('change')">
    <option value="">{{ t("billing.subscriptions.change.auto") }}</option>
    <option value="CHANGE_TIMING_IMMEDIATELY">{{ t("billing.subscriptions.change.now") }}</option>
    <option value="CHANGE_TIMING_PERIOD_END">{{ t("billing.subscriptions.change.atEnd") }}</option>
  </NativeSelect>
  <NativeSelect v-model="proration" :aria-label="t('billing.subscriptions.change.proration')" @change="emit('change')">
    <option value="">{{ t("billing.subscriptions.change.prorate") }}</option>
    <option value="CHANGE_PRORATION_FULL">{{ t("billing.subscriptions.change.full") }}</option>
    <option value="CHANGE_PRORATION_NONE">{{ t(noneKey) }}</option>
  </NativeSelect>
</template>
