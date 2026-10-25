<script setup lang="ts">
import { ChevronLeft, ChevronRight } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";

/** Previous and next for a token-paged list, with how many there are in all. */
defineProps<{ hasPrev: boolean; next: string; total: number }>();
defineEmits<{ prev: []; next: [] }>();
const { t } = useI18n();
</script>

<template>
  <div v-if="hasPrev || next" class="flex flex-wrap items-center justify-between gap-2 px-3 pt-2">
    <span class="text-muted-foreground text-xs">{{ t("affiliates.common.total", { count: total }) }}</span>
    <div class="flex gap-2">
      <Button type="button" variant="outline" size="sm" :disabled="!hasPrev" @click="$emit('prev')"><ChevronLeft class="size-4 rtl:rotate-180" />{{ t("affiliates.common.prev") }}</Button>
      <Button type="button" variant="outline" size="sm" :disabled="!next" @click="$emit('next')">{{ t("affiliates.common.next") }}<ChevronRight class="size-4 rtl:rotate-180" /></Button>
    </div>
  </div>
  <p v-else-if="total > 0" class="text-muted-foreground px-3 pt-2 text-xs">{{ t("affiliates.common.total", { count: total }) }}</p>
</template>
