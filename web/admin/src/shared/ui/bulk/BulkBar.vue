<script setup lang="ts">
import { X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";

/**
 * The bar above a table while rows are ticked: how many, the bulk actions the page puts in the default slot, and a button to untick everything. The page
 * decides what an action does to each row and says what happened to every one (see runBulk).
 */
defineProps<{ count: number }>();
defineEmits<{ clear: [] }>();
const { t } = useI18n();
</script>

<template>
  <div v-if="count > 0" class="bg-brand-soft text-brand-strong mx-3 mb-3 flex flex-wrap items-center gap-2 rounded-2xl px-4 py-2 text-sm" role="region" :aria-label="t('common.bulk.label')" data-testid="bulk-bar">
    <span class="font-medium" data-testid="bulk-count">{{ t("common.bulk.selected", { n: count }, count) }}</span>
    <div class="flex flex-wrap items-center gap-2"><slot /></div>
    <Button type="button" variant="ghost" size="sm" class="ms-auto" data-testid="bulk-clear" @click="$emit('clear')"><X class="size-4" aria-hidden="true" />{{ t("common.bulk.clear") }}</Button>
  </div>
</template>
