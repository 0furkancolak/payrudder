<script setup lang="ts">
import { ChevronLeft, ChevronRight, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { ahead, back, forward, frontUid, openTabs, raise } from "@/shared/lib/sheets";
import { cn } from "@/shared/lib/utils";

/**
 * The strip at the top of the sheet in front, shown while more than one sheet is open or one was just closed with "back": the open sheets as tabs
 * (raise one by choosing it, close one with its cross), and the arrows that close the sheet in front or open again the one closed last.
 */
const { t } = useI18n();
</script>

<template>
  <div v-if="openTabs.length > 1 || ahead.length > 0" class="bg-muted/40 flex items-center gap-1 border-b py-1.5 ps-3 pe-14" data-testid="sheet-strip">
    <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="openTabs.length === 0" :aria-label="t('sheets.back')" data-testid="sheet-back" @click="back()"><ChevronLeft class="size-4 rtl:rotate-180" aria-hidden="true" /></button>
    <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="ahead.length === 0" :aria-label="t('sheets.forward')" data-testid="sheet-forward" @click="forward()"><ChevronRight class="size-4 rtl:rotate-180" aria-hidden="true" /></button>
    <div class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto" role="tablist">
      <div
        v-for="tab in openTabs"
        :key="tab.uid"
        role="presentation"
        :class="cn('flex max-w-44 shrink-0 items-center gap-1 rounded-full border ps-3 pe-1 text-xs', tab.uid === frontUid ? 'bg-card text-foreground font-medium shadow-xs' : 'text-muted-foreground hover:bg-muted border-transparent')"
        data-testid="sheet-tab"
      >
        <button type="button" role="tab" :aria-selected="tab.uid === frontUid" class="min-w-0 truncate py-1 text-start" @click="raise(tab.uid)">{{ tab.title || "…" }}</button>
        <button type="button" class="hover:bg-muted rounded-full p-1" :aria-label="t('common.close')" @click="tab.close()"><X class="size-3" aria-hidden="true" /></button>
      </div>
    </div>
  </div>
</template>
