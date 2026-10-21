<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { zoneList } from "@/shared/lib/intl";
import SearchSelect from "./SearchSelect.vue";

/** Every IANA time zone the browser knows, with its offset from UTC right now; typing "istanbul", "+03" or "tokyo" finds it. */
defineProps<{ id?: string; ariaLabel?: string; disabled?: boolean; class?: string }>();
const model = defineModel<string>({ required: true });
const { t } = useI18n();

const options = computed(() => zoneList().map((z) => ({ value: z.id, label: z.id.replace(/_/g, " "), short: z.id, hint: z.offset, keywords: z.id.replace(/[_/]/g, " ") })));
</script>

<template>
  <SearchSelect :id="id" v-model="model" :options="options" :aria-label="ariaLabel ?? t('picker.timezone')" :placeholder="t('picker.timezonePlaceholder')" :empty-text="t('picker.noMatch')" :disabled="disabled" :class="$props.class" />
</template>
