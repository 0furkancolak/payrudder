<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { MODES, WINDOWS, modeWord, windowWord, type QuotaDraft } from "./quota";

/** The terms of one quota: the limit, the window it counts over, what reaching it does, and the alert thresholds. `n` numbers the fields of a list of them. */
const props = defineProps<{ unit?: string; n?: number }>();
const model = defineModel<QuotaDraft>({ required: true });
const { t } = useI18n();
const label = (key: string) => (props.n ? t(`billing.quotas.${key}N`, { n: props.n }) : t(`billing.quotas.${key}`));
</script>

<template>
  <div class="grid gap-2 sm:grid-cols-2">
    <Input v-model="model.limit" inputmode="decimal" :aria-label="label('limit')" :placeholder="unit ? t('billing.quotas.limitIn', { unit }) : t('billing.quotas.limitPlaceholder')" required />
    <NativeSelect v-model="model.window" :aria-label="label('window')">
      <option v-for="w in WINDOWS" :key="w" :value="w">{{ t(`billing.quotas.windows.${windowWord(w)}`) }}</option>
    </NativeSelect>
    <NativeSelect v-model="model.mode" :aria-label="label('mode')">
      <option v-for="m in MODES" :key="m" :value="m">{{ t(`billing.quotas.modes.${modeWord(m)}`) }}</option>
    </NativeSelect>
    <Input v-model="model.thresholds" :aria-label="label('thresholds')" :placeholder="t('billing.quotas.thresholdsPlaceholder')" />
    <p class="text-muted-foreground text-xs sm:col-span-2">{{ t(`billing.quotas.modeHelp.${modeWord(model.mode)}`) }} {{ t(`billing.quotas.windowHelp.${windowWord(model.window)}`) }}</p>
  </div>
</template>
