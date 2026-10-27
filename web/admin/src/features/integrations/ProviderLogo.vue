<script setup lang="ts">
import { computed } from "vue";
import { cn } from "@/shared/lib/utils";
import { BRANDS, type IntegrationId } from "./brands";

/**
 * The mark of an integration: the licensed or official logo of a company (light and dark files, switched by the theme the way the product's own logo is), or a
 * general-purpose icon for a method with no company behind it. A company without a licensed mark has no tile here at all: the card shows its plain name.
 * Nothing is drawn by hand in a logo's place.
 */
const props = defineProps<{ id: IntegrationId; class?: string }>();
const brand = computed(() => BRANDS[props.id]);
const box = "bg-muted flex h-11 shrink-0 items-center justify-center rounded-2xl";
// A wordmark is wider than tall: it gets a wider tile (never wider than a third of a card) and keeps its proportions.
const wide = computed(() => !!brand.value.wide);
</script>

<template>
  <span v-if="brand.logo" :class="cn(box, wide ? 'w-28 max-w-[40%] px-2.5 py-2' : 'size-11 p-2.5', props.class)" :data-logo="id">
    <img :src="brand.logo.light" alt="" :class="cn('size-full object-contain', brand.logo.light !== brand.logo.dark && 'logo-on-light')" draggable="false" />
    <img v-if="brand.logo.light !== brand.logo.dark" :src="brand.logo.dark" alt="" aria-hidden="true" class="logo-on-dark size-full object-contain" draggable="false" />
  </span>
  <span v-else-if="brand.icon" :class="cn(box, 'size-11 text-muted-foreground', props.class)" :data-icon="id" aria-hidden="true"><component :is="brand.icon" class="size-5" /></span>
</template>
