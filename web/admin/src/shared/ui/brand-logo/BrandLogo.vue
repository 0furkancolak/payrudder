<script setup lang="ts">
import { computed } from "vue";
import { brandName } from "@/shared/lib/brand";
import { cn } from "@/shared/lib/utils";

/**
 * The product logo (payrudder-brand/DESIGN.md section 2). `wordmark` is the mark with the name, `icon` the app icon alone. On the dark icon rail
 * (`on="rail"`) the dark-background files are used in both themes; on the page (`on="page"`) the light and dark versions switch with the theme
 * (.logo-on-light / .logo-on-dark in styles.css). A deployment that renames the product with BRAND_NAME gets a plain letter tile and the name instead
 * of the Payrudder artwork.
 */
const props = withDefaults(defineProps<{ variant?: "wordmark" | "icon"; on?: "page" | "rail"; class?: string }>(), { variant: "wordmark", on: "page" });
const brand = brandName();
const ours = brand === "Payrudder";
const wordmark = computed(() => cn("h-7 w-auto", props.class));
const icon = computed(() => cn("size-10 shrink-0", props.class));
</script>

<template>
  <template v-if="ours">
    <template v-if="variant === 'wordmark'">
      <img v-if="on === 'rail'" src="/brand/payrudder-wordmark-dark.svg" :alt="brand" :class="wordmark" />
      <template v-else>
        <img src="/brand/payrudder-wordmark-light.svg" :alt="brand" :class="['logo-on-light', wordmark]" />
        <img src="/brand/payrudder-wordmark-dark.svg" alt="" aria-hidden="true" :class="['logo-on-dark', wordmark]" />
      </template>
    </template>
    <img v-else src="/brand/payrudder-icon.svg" :alt="brand" :class="icon" />
  </template>
  <span v-else class="inline-flex items-center gap-2.5 text-lg font-semibold tracking-tight">
    <span class="bg-brand text-rail flex size-9 shrink-0 items-center justify-center rounded-xl text-base font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
    <span v-if="variant === 'wordmark'" :class="on === 'rail' ? 'text-rail-accent-foreground' : ''">{{ brand }}</span>
    <span v-else class="sr-only">{{ brand }}</span>
  </span>
</template>
