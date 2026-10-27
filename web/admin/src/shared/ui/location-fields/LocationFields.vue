<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { countryProfile } from "@/shared/lib/address";
import { districtOptions, findRegion, regionOptions, typedPlace } from "@/shared/lib/geo";
import { Input } from "@/shared/ui/input";
import { SearchSelect } from "@/shared/ui/search-select";
import { useGeoDistricts, useGeoRegions } from "./api";
import CountrySelect from "./CountrySelect.vue";

/**
 * The country, the region and the city of an address, one after the other: the country from the whole list, then the regions of that country, then the
 * districts and towns of that region, each searchable, each also taking free text (a town the list does not have). When a country has no regions in the
 * list, or a region no districts, the field is plain text. The address keeps its shape: the country code and the region and city as text, so an address
 * saved before the lists existed loads and saves as it was.
 */
const props = defineProps<{
  /** Prefix of the fields' ids (`${idPrefix}-country`, `-region`, `-city`). */
  idPrefix: string;
  /** No visible labels: the fields carry their names as aria-label and placeholder (the narrow customer sheets). */
  compact?: boolean;
  /** Other names for the fields than the address labels. */
  labels?: { country?: string; region?: string; city?: string };
  /** A translated problem per field, shown under it. */
  problems?: Partial<Record<"country" | "region" | "city", string>>;
  disabled?: boolean;
}>();
const country = defineModel<string>("country", { default: "" });
const region = defineModel<string>("region", { default: "" });
const city = defineModel<string>("city", { default: "" });
const { t } = useI18n();

const code = computed(() => (country.value ?? "").trim().toUpperCase());
const profile = computed(() => countryProfile(code.value));
const regionsQuery = useGeoRegions(code);
const regions = computed(() => (regionsQuery.isError.value ? [] : (regionsQuery.data.value?.regions ?? [])));
const matched = computed(() => findRegion(regions.value, region.value));
const districtsQuery = useGeoDistricts(computed(() => matched.value?.id ?? ""));
const districts = computed(() => (districtsQuery.isError.value || !matched.value ? [] : (districtsQuery.data.value?.districts ?? [])));
const regionOpts = computed(() => regionOptions(regions.value));
const districtOpts = computed(() => districtOptions(districts.value));

/** A region field shows where the country has regions in the list, where its addresses usually name one, or when one is already filled in. */
const showRegion = computed(() => regions.value.length > 0 || profile.value.usesRegion || (region.value ?? "") !== "");

const labelOf = {
  country: () => props.labels?.country ?? t("address.labels.country"),
  region: () => props.labels?.region ?? t(`address.labels.${profile.value.region}`),
  city: () => props.labels?.city ?? t("address.labels.city"),
};
const typed = (q: string) => typedPlace(q, (value) => t("picker.useTyped", { value }));

// Another country makes the region and the city of the old one meaningless; another region does the same to the city.
function setCountry(v: string) {
  if (v === country.value) return;
  country.value = v;
  region.value = "";
  city.value = "";
}
function setRegion(v: string) {
  if (v === region.value) return;
  const before = matched.value?.id;
  region.value = v;
  if (before !== undefined && findRegion(regions.value, v)?.id !== before) city.value = "";
}
</script>

<template>
  <div class="grid gap-3" data-testid="location-fields">
    <div class="grid gap-1">
      <label v-if="!compact" class="text-sm" :for="`${idPrefix}-country`">{{ labelOf.country() }}</label>
      <CountrySelect
        :id="`${idPrefix}-country`"
        :model-value="country"
        :aria-label="labelOf.country()"
        :blank="compact ? undefined : t('address.noCountry')"
        :placeholder="compact ? labelOf.country() : undefined"
        :invalid="!!problems?.country"
        :disabled="disabled"
        @update:model-value="setCountry"
      />
      <p v-if="problems?.country" class="text-destructive text-xs" role="alert">{{ problems.country }}</p>
    </div>
    <div :class="compact ? 'grid gap-3' : 'grid gap-3 sm:grid-cols-2'">
      <div v-if="showRegion" class="grid gap-1">
        <label v-if="!compact" class="text-sm" :for="`${idPrefix}-region`">{{ labelOf.region() }}</label>
        <SearchSelect
          v-if="regionOpts.length > 0"
          :id="`${idPrefix}-region`"
          :model-value="region"
          :options="regionOpts"
          :custom="typed"
          :aria-label="labelOf.region()"
          :placeholder="compact ? labelOf.region() : t('picker.placePlaceholder')"
          :empty-text="t('picker.noMatch')"
          :invalid="!!problems?.region"
          :disabled="disabled"
          @update:model-value="setRegion"
        />
        <Input
          v-else
          :id="`${idPrefix}-region`"
          :model-value="region"
          maxlength="120"
          autocomplete="address-level1"
          :aria-label="labelOf.region()"
          :placeholder="compact ? labelOf.region() : undefined"
          :aria-invalid="!!problems?.region"
          :disabled="disabled"
          @update:model-value="setRegion(String($event))"
        />
        <p v-if="problems?.region" class="text-destructive text-xs" role="alert">{{ problems.region }}</p>
      </div>
      <div class="grid gap-1">
        <label v-if="!compact" class="text-sm" :for="`${idPrefix}-city`">{{ labelOf.city() }}</label>
        <SearchSelect
          v-if="districtOpts.length > 0"
          :id="`${idPrefix}-city`"
          v-model="city"
          :options="districtOpts"
          :custom="typed"
          :aria-label="labelOf.city()"
          :placeholder="compact ? labelOf.city() : t('picker.placePlaceholder')"
          :empty-text="t('picker.noMatch')"
          :invalid="!!problems?.city"
          :disabled="disabled"
        />
        <Input
          v-else
          :id="`${idPrefix}-city`"
          v-model="city"
          maxlength="120"
          autocomplete="address-level2"
          :aria-label="labelOf.city()"
          :placeholder="compact ? labelOf.city() : undefined"
          :aria-invalid="!!problems?.city"
          :disabled="disabled"
        />
        <p v-if="problems?.city" class="text-destructive text-xs" role="alert">{{ problems.city }}</p>
      </div>
    </div>
  </div>
</template>
