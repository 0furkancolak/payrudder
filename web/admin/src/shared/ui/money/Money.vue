<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { privacyMode } from "@/shared/lib/prefs";

/**
 * An amount, formatted in the currency's own number of digits for the page's language. In privacy mode the figures are a mask, and the element says "hidden"
 * to assistive technology instead of reading out bullets. Every place that shows an amount as a figure uses this or `formatMinor` (which masks the same way).
 */
const props = defineProps<{ minor: string | number | null | undefined; currency: string | undefined }>();
const { t, locale } = useI18n();
const text = computed(() => (props.minor === null || props.minor === undefined ? "—" : formatMinor(props.minor, props.currency, locale.value)));
</script>

<template>
  <span class="tabular-nums" :data-private="privacyMode ? '' : undefined" :aria-label="privacyMode && minor !== null && minor !== undefined ? t('privacy.hidden') : undefined" :title="privacyMode ? t('privacy.hidden') : undefined">{{ text }}</span>
</template>
