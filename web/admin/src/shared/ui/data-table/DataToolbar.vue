<script setup lang="ts">
import { SlidersHorizontal, Search, X } from "@lucide/vue";
import { useMediaQuery } from "@vueuse/core";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/shared/ui/dialog";
import { provideApplied } from "@/shared/ui/filter-select/applied";
import { Input } from "@/shared/ui/input";

/**
 * The row above a table: a search box, then the filters the page puts in the default slot (selects), a clear button while something is filtered, and an
 * `actions` slot at the end for the page's main button. On a phone the search stays in the row and the filters move into a drawer behind one button, so
 * the row does not fill the screen; the clear button is in the drawer, and the filters that are on are listed under the row as removable chips (they report
 * themselves, see filter-select/applied.ts), so what the list is narrowed by stays in sight while the drawer is shut. The drawer stays mounted while closed (hidden), so whatever a page puts in the filter slot besides filters (its main button, which draws in the page header) is still there.
 */
defineProps<{ placeholder?: string; active?: boolean }>();
const search = defineModel<string>("search", { default: "" });
defineEmits<{ clear: [] }>();
const { t } = useI18n();
const phone = useMediaQuery("(max-width: 767px)");
const drawer = ref(false);
const applied = provideApplied();
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 px-1 py-2 sm:px-3 sm:py-3" role="search">
    <div class="relative min-w-40 flex-1 sm:max-w-xs">
      <Search class="text-muted-foreground pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2" aria-hidden="true" />
      <Input v-model="search" type="search" :aria-label="placeholder ?? t('common.search')" :placeholder="placeholder ?? t('common.search')" class="ps-9" />
    </div>

    <template v-if="phone">
      <Button v-if="$slots.default" type="button" variant="outline" size="icon" class="relative shrink-0 rounded-full" :aria-label="t('common.filters')" data-testid="filters-open" @click="drawer = true">
        <SlidersHorizontal class="size-4" aria-hidden="true" />
        <span v-if="active" class="bg-brand absolute end-2 top-2 size-2 rounded-full" aria-hidden="true" />
      </Button>
      <Dialog v-model:open="drawer">
        <DialogContent force-mount class="data-[state=closed]:hidden top-auto bottom-0 left-0 max-h-[85dvh] max-w-none translate-x-0 translate-y-0 gap-3 overflow-y-auto rounded-b-none rounded-t-3xl p-5 data-[state=closed]:slide-out-to-bottom data-[state=open]:slide-in-from-bottom sm:max-w-none" data-testid="filters-drawer">
          <DialogHeader class="text-start">
            <DialogTitle>{{ t("common.filters") }}</DialogTitle>
            <DialogDescription class="sr-only">{{ t("common.filters") }}</DialogDescription>
          </DialogHeader>
          <div class="flex flex-wrap items-center gap-2"><slot /></div>
          <div class="flex items-center justify-between gap-2 pt-1">
            <Button v-if="active" type="button" variant="ghost" @click="$emit('clear')"><X class="size-4" aria-hidden="true" />{{ t("common.clearFilters") }}</Button>
            <Button type="button" class="ms-auto" @click="drawer = false">{{ t("common.showResults") }}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </template>
    <template v-else>
      <slot />
      <Button v-if="active" type="button" variant="ghost" size="sm" @click="$emit('clear')"><X class="size-4" aria-hidden="true" />{{ t("common.clearFilters") }}</Button>
    </template>
    <div class="ms-auto flex max-w-full flex-wrap items-center justify-end gap-2"><slot name="actions" /></div>
    <ul v-if="phone && applied.length > 0" class="m-0 flex basis-full list-none flex-wrap gap-1.5 p-0" :aria-label="t('common.appliedFilters')" data-testid="applied-filters">
      <li v-for="a in applied" :key="a.id" class="max-w-full">
        <button type="button" class="bg-brand-soft text-brand-strong inline-flex max-w-full items-center gap-1 rounded-full py-1 ps-3 pe-2 text-xs font-medium" :aria-label="`${a.label}: ${a.text} ×`" data-testid="applied-filter" @click="a.clear()">
          <span class="truncate">{{ a.label }}: {{ a.text }}</span><X class="size-3.5 shrink-0" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </div>
</template>
