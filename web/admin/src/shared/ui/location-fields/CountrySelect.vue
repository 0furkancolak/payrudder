<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { countryOptions, typedCountry } from "@/shared/lib/geo";
import { SearchSelect } from "@/shared/ui/search-select";
import { useGeoCountries } from "./api";

/**
 * Every country (GeoNames), named in the page's language and searchable by that name, the English name and the codes. Any other two-letter code can be
 * typed (the API accepts any ISO 3166-1 code), and a stored code that is not in the list still shows. `blank` adds an empty choice ("Any country").
 */
const props = defineProps<{ id?: string; ariaLabel?: string; disabled?: boolean; invalid?: boolean; class?: string; blank?: string; placeholder?: string; codeOnly?: boolean }>();
const model = defineModel<string>({ required: true });
const { t, locale } = useI18n();
const countries = useGeoCountries();

const options = computed(() => {
  const list = countryOptions(countries.data.value?.countries ?? [], locale.value, props.codeOnly);
  return props.blank ? [{ value: "", label: props.blank, short: props.blank }, ...list] : list;
});
const custom = (q: string) => typedCountry(q, (code) => t("picker.useCode", { code }));
</script>

<template>
  <SearchSelect
    :id="id"
    v-model="model"
    :options="options"
    :custom="custom"
    :aria-label="ariaLabel ?? t('address.labels.country')"
    :placeholder="placeholder ?? blank ?? t('picker.countryPlaceholder')"
    :empty-text="t('picker.noMatch')"
    :disabled="disabled"
    :invalid="invalid"
    :class="$props.class"
  />
</template>
