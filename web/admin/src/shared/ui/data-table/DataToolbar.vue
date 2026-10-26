<script setup lang="ts">
import { Search, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

/**
 * The row above a table: a search box, then the filters the page puts in the default slot (selects), a clear button while something is filtered, and an
 * `actions` slot at the end for the page's main button.
 */
defineProps<{ placeholder?: string; active?: boolean }>();
const search = defineModel<string>("search", { default: "" });
defineEmits<{ clear: [] }>();
const { t } = useI18n();
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 px-3 pb-3" role="search">
    <div class="relative min-w-48 flex-1 sm:max-w-xs">
      <Search class="text-muted-foreground pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2" aria-hidden="true" />
      <Input v-model="search" type="search" :aria-label="placeholder ?? t('common.search')" :placeholder="placeholder ?? t('common.search')" class="ps-9" />
    </div>
    <slot />
    <Button v-if="active" type="button" variant="ghost" size="sm" @click="$emit('clear')"><X class="size-4" aria-hidden="true" />{{ t("common.clearFilters") }}</Button>
    <div class="ms-auto flex max-w-full flex-wrap items-center justify-end gap-2"><slot name="actions" /></div>
  </div>
</template>
