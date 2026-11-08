<script setup lang="ts">
import { computed } from "vue";
import { brandName } from "@/shared/lib/brand";
import { cn } from "@/shared/lib/utils";

/**
 * The product logo (payrudder-brand/DESIGN.md section 2). `wordmark` is the mark with the name, `icon` the app icon alone. The light and dark versions
 * switch with the theme (.logo-on-light / .logo-on-dark in styles.css), on the page and in the sidebar alike. A deployment that renames the product with
 * BRAND_NAME gets a plain letter tile and the name instead of the Payrudder artwork.
 */
const props = withDefaults(defineProps<{ variant?: "wordmark" | "icon"; class?: string }>(), { variant: "wordmark" });
const brand = brandName();
const ours = brand === "Payrudder";
const wordmark = computed(() => cn("h-7 w-auto", props.class));
const icon = computed(() => cn("size-10 shrink-0", props.class));
</script>

<template>
  <template v-if="ours">
    <template v-if="variant === 'wordmark'">
      <img src="/brand/payrudder-wordmark-light.svg" :alt="brand" :class="['logo-on-light', wordmark]" />
      <img src="/brand/payrudder-wordmark-dark.svg" alt="" aria-hidden="true" :class="['logo-on-dark', wordmark]" />
    </template>
    <img v-else src="/brand/payrudder-icon.svg" :alt="brand" :class="icon" />
  </template>
  <span v-else class="inline-flex items-center gap-2.5 text-lg font-semibold tracking-tight">
    <span class="bg-primary text-primary-foreground flex size-9 shrink-0 items-center justify-center rounded-xl text-base font-bold" aria-hidden="true">{{ brand.slice(0, 1) }}</span>
    <span v-if="variant === 'wordmark'">{{ brand }}</span>
    <span v-else class="sr-only">{{ brand }}</span>
  </span>
</template>
