<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { currencyList } from "@/shared/lib/intl";
import SearchSelect from "./SearchSelect.vue";

/** Every currency the backend accepts: the code, the name in the page's language and the symbol, searchable by any of them. */
const props = defineProps<{ id?: string; ariaLabel?: string; disabled?: boolean; class?: string; only?: readonly string[]; blank?: string }>();
const model = defineModel<string>({ required: true });
const { t, locale } = useI18n();

const base = computed(() => {
  const list = currencyList(locale.value).map((c) => ({ value: c.code, label: `${c.code} · ${c.name}`, short: c.code, hint: c.symbol === c.code ? "" : c.symbol, keywords: c.symbol }));
  return props.blank ? [{ value: "", label: props.blank, short: props.blank, hint: "", keywords: "" }, ...list] : list;
});
</script>

<template>
  <SearchSelect
    :id="id"
    v-model="model"
    :options="props.only ? base.filter((o) => o.value === '' || props.only?.includes(o.value)) : base"
    :aria-label="ariaLabel ?? t('picker.currency')"
    :placeholder="t('picker.currencyPlaceholder')"
    :empty-text="t('picker.noMatch')"
    :disabled="disabled"
    :class="$props.class"
  />
</template>
