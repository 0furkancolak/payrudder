<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { CREDITS_OR_ZERO_RE } from "@/shared/lib/rules";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { useCreditSettings, useSetCreditSettings } from "./api";

/** The balance below which a consume sends the credits.low webhook event (empty or 0: off). */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const settings = useCreditSettings(() => props.tenantId);
const setSettings = useSetCreditSettings(() => props.tenantId);
const threshold = ref("");
watch(
  () => settings.data.value?.low_threshold,
  (v) => {
    threshold.value = v && v !== "0" ? v : "";
  },
  { immediate: true },
);
const thresholdValid = computed(() => threshold.value.trim() === "" || CREDITS_OR_ZERO_RE.test(threshold.value.trim()));
function save() {
  if (thresholdValid.value) setSettings.mutate(threshold.value.trim() || "0");
}
</script>

<template>
  <Card>
    <h2 class="text-base font-semibold">{{ t("billing.credits.settings.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.credits.settings.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-3" role="form" :aria-label="t('billing.credits.settings.formLabel')" @submit.prevent="save">
      <Input v-model="threshold" inputmode="numeric" :aria-label="t('billing.credits.settings.threshold')" :placeholder="t('billing.credits.settings.threshold')" />
      <Button type="submit" :disabled="setSettings.isPending.value || !thresholdValid">{{ t("billing.credits.settings.save") }}</Button>
      <span v-if="setSettings.isSuccess.value" class="text-muted-foreground self-center text-sm" role="status">{{ t("billing.credits.settings.saved") }}</span>
    </form>
    <ErrorText :error="setSettings.error.value" />
  </Card>
</template>
