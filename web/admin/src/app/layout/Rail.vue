<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import type { Component } from "vue";

/**
 * The dark, rounded, icon-only rail. On a wide screen it floats on the start side; on a phone it becomes a bar along the bottom. Every item has a text
 * label for assistive technology and a tooltip (title) for the mouse; the active page is marked with aria-current and a filled tile.
 */
defineProps<{ items: { to: string; label: string; icon: Component; exact?: boolean }[]; brand: string }>();

const tile =
  "text-rail-foreground hover:text-white hover:bg-white/10 relative flex size-11 items-center justify-center rounded-2xl transition-colors focus-visible:outline-2 focus-visible:outline-offset-2";
const tileActive = "!bg-brand !text-rail shadow-[0_6px_16px_-6px_var(--brand)]";
</script>

<template>
  <aside class="bg-rail fixed inset-x-4 bottom-4 z-40 flex items-center justify-around rounded-[26px] px-3 py-2 shadow-pop md:inset-x-auto md:start-4 md:inset-y-4 md:w-[76px] md:flex-col md:justify-start md:gap-2 md:px-0 md:py-5">
    <div class="bg-brand text-rail hidden size-11 items-center justify-center rounded-2xl text-lg font-bold md:mb-4 md:flex" aria-hidden="true">{{ brand.slice(0, 1) }}</div>
    <nav class="flex items-center justify-around gap-1 max-md:flex-1 md:flex-col md:gap-2" :aria-label="brand">
      <Link v-for="item in items" :key="item.to" :to="item.to" :class="tile" :active-props="{ class: tileActive, 'aria-current': 'page' }" :active-options="{ exact: item.exact ?? false }" :title="item.label">
        <component :is="item.icon" class="size-5" aria-hidden="true" />
        <span class="sr-only">{{ item.label }}</span>
      </Link>
    </nav>
    <div class="hidden md:mt-auto md:flex md:flex-col md:gap-2"><slot name="bottom" /></div>
  </aside>
</template>
