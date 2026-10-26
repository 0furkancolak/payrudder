<script setup lang="ts">
import { ChevronDown, ChevronUp } from "@lucide/vue";
import { onBeforeUnmount, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";

/**
 * The detail of one row of a list, in a sheet beside the list instead of a page of its own. The header says where the row is in the list ("3 / 25") and
 * has the arrows to the previous and next row, which also answer to the up and down keys while no field has focus, so a person can go down a list
 * reading each one. `index` counts from 0 over all `total` rows the list holds now (filtered, across its pages: useTableState moves the table to the
 * page of the row shown); a null index is closed.
 *
 * A list the console holds only one server page of passes the rows it has as `total`, how many rows come before them as `offset` and how many there
 * are in all as `of`, and says with `edge` that a page lies beyond; stepping past the loaded rows then emits `edge` ("prev" or "next") for the list to
 * load that page and put the index on its near row.
 */
const props = defineProps<{ index: number | null; total: number; title: string; description?: string; wide?: boolean; offset?: number; of?: number; edge?: { prev?: boolean; next?: boolean } }>();
const emit = defineEmits<{ "update:index": [number | null]; edge: ["prev" | "next"] }>();
const { t } = useI18n();

const open = () => props.index !== null;
const hasPrev = () => props.index !== null && (props.index > 0 || !!props.edge?.prev);
const hasNext = () => props.index !== null && (props.index < props.total - 1 || !!props.edge?.next);
function go(by: -1 | 1) {
  if (props.index === null) return;
  const to = props.index + by;
  if (to >= 0 && to < props.total) emit("update:index", to);
  else if (by < 0 && props.edge?.prev) emit("edge", "prev");
  else if (by > 0 && props.edge?.next) emit("edge", "next");
}

function key(e: KeyboardEvent) {
  if (props.index === null) return;
  const el = e.target as HTMLElement | null;
  if (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) return;
  if (el?.isContentEditable) return;
  if (e.key === "ArrowDown" && hasNext()) {
    e.preventDefault();
    go(1);
  } else if (e.key === "ArrowUp" && hasPrev()) {
    e.preventDefault();
    go(-1);
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
          <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="!hasPrev()" :aria-label="t('pagination.prev')" data-testid="detail-prev" @click="go(-1)"><ChevronUp class="size-4" aria-hidden="true" /></button>
          <button type="button" class="hover:bg-muted rounded-full p-1 disabled:opacity-40" :disabled="!hasNext()" :aria-label="t('pagination.next')" data-testid="detail-next" @click="go(1)"><ChevronDown class="size-4" aria-hidden="true" /></button>
          <span class="tabular ms-1" data-testid="detail-position">{{ (offset ?? 0) + index + 1 }} / {{ of ?? total }}</span>
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
