<script setup lang="ts">
import { ChevronDown, ChevronUp } from "@lucide/vue";
import { onBeforeUnmount, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";

/**
 * The detail of one row of a list, in a sheet beside the list instead of a page of its own. The header says where the row is in the list ("3 / 25") and
 * has the arrows to the previous and next row, which also answer to the up and down keys while no field has focus, so a person can go down a list
 * reading each one. `index` counts from 0 over the rows the list shows now (filtered and on this page); a null index is closed.
 */
const props = defineProps<{ index: number | null; total: number; title: string; description?: string; wide?: boolean }>();
const emit = defineEmits<{ "update:index": [number | null] }>();
const { t } = useI18n();

const open = () => props.index !== null;
const go = (to: number) => emit("update:index", to >= 0 && to < props.total ? to : props.index);

function key(e: KeyboardEvent) {
  if (props.index === null) return;
  const el = e.target as HTMLElement | null;
  if (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) return;
  if (el?.isContentEditable) return;
  if (e.key === "ArrowDown" && props.index < props.total - 1) {
    e.preventDefault();
    go(props.index + 1);
  } else if (e.key === "ArrowUp" && props.index > 0) {
    e.preventDefault();
    go(props.index - 1);
  }
}
onMounted(() => window.addEventListener("keydown", key));
onBeforeUnmount(() => window.removeEventListener("keydown", key));
</script>

<template>
  <Sheet :open="open()" @update:open="(v) => !v && emit('update:index', null)">
    <SheetContent :class="wide ? 'sm:max-w-2xl' : 'sm:max-w-lg'" data-testid="detail-sheet">
      <SheetHeader>
        <div v-if="index !== null" class="text-muted-foreground mb-1 flex items-center gap-1 text-xs">
          <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="index <= 0" :aria-label="t('pagination.prev')" data-testid="detail-prev" @click="go(index - 1)"><ChevronUp class="size-4" aria-hidden="true" /></button>
          <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="index >= total - 1" :aria-label="t('pagination.next')" data-testid="detail-next" @click="go(index + 1)"><ChevronDown class="size-4" aria-hidden="true" /></button>
          <span class="tabular ms-1" data-testid="detail-position">{{ index + 1 }} / {{ total }}</span>
        </div>
        <SheetTitle>{{ title }}</SheetTitle>
        <SheetDescription v-if="description">{{ description }}</SheetDescription>
        <SheetDescription v-else class="sr-only">{{ title }}</SheetDescription>
      </SheetHeader>
      <SheetBody><slot /></SheetBody>
      <SheetFooter v-if="$slots.footer"><slot name="footer" /></SheetFooter>
    </SheetContent>
  </Sheet>
</template>
