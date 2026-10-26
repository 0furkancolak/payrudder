<script setup lang="ts">
import { X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";

/**
 * The row above a list the server filters and pages (no search box: the API has none): the filters in the default slot, a clear button while something
 * is filtered, and the page's main button in `actions`, at the end of the same row. It lines up with DataToolbar.
 */
defineProps<{ active?: boolean }>();
defineEmits<{ clear: [] }>();
const { t } = useI18n();
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 px-3 pb-3" role="search">
    <slot />
    <Button v-if="active" type="button" variant="ghost" size="sm" @click="$emit('clear')"><X class="size-4" aria-hidden="true" />{{ t("common.clearFilters") }}</Button>
    <div class="ms-auto flex items-center gap-2"><slot name="actions" /></div>
  </div>
</template>
