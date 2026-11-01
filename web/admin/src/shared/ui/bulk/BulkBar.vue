<script setup lang="ts">
import { Loader, X } from "@lucide/vue";
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";

/**
 * The bar above a table while rows are ticked: how many, the bulk actions the page puts in the default slot, and a button to untick everything. The page
 * decides what an action does to each row and says what happened to every one (see runBulk).
 *
 * When the ticked page is not all there is (`offer`), the bar offers "Select all N results" (N is `total`, the rows the search and filters show on every page);
 * with `more` the console holds only the first server page of the list, so the button says "all matching results" and loads the rest first (see useBulk).
 *
 * Keyboard and screen reader: the bar is a landmark (`region`) placed before the table, so its buttons are the next stop for Tab after the header; Escape unticks
 * everything (not while a popover, menu, sheet or dialog is open: that one takes the Escape) and puts the focus back on the header's tick; a polite live region, present even while
 * nothing is ticked, announces the number of rows ticked and when the selection is cleared.
 */
const props = defineProps<{ count: number; total?: number; offer?: boolean; more?: boolean; loading?: boolean }>();
const emit = defineEmits<{ clear: []; "select-all": [] }>();
const { t } = useI18n();

const root = ref<HTMLElement | null>(null);
const announcement = ref("");
watch(
  () => props.count,
  (n, was) => {
    announcement.value = n > 0 ? t("common.bulk.announce", { n }, n) : was > 0 ? t("common.bulk.cleared") : "";
  },
);

/** Unticks everything; when the focus was in the bar (which then goes away) it moves to the table's header tick so a keyboard user does not lose their place. */
function clear() {
  const host = root.value?.parentElement;
  const hadFocus = root.value?.contains(document.activeElement) ?? false;
  emit("clear");
  if (hadFocus) void nextTick(() => host?.querySelector<HTMLElement>('thead [role="checkbox"]')?.focus());
}

/** What an Escape belongs to while it is open: a sheet, dialog or popover, a menu, a list. */
const LAYER = '[role="dialog"][data-state="open"],[role="alertdialog"][data-state="open"],[role="menu"][data-state="open"],[role="listbox"]';

/**
 * Escape from a tick, the bar or anything else in the table's card. Not from a text field (it is the field's own: a search box clears its text) and not while a
 * layer is open: the layers close on the same key press (they listen on the window too, and do not mark the event), so the bar waits for the next one.
 */
function onKey(e: KeyboardEvent) {
  if (e.key !== "Escape" || props.count === 0) return;
  const el = e.target instanceof HTMLElement ? e.target : null;
  if (!el || !root.value?.parentElement?.contains(el)) return;
  if (!root.value.contains(el) && (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable)) return;
  if (document.querySelector(LAYER)) return;
  clear();
}
onMounted(() => window.addEventListener("keydown", onKey));
onBeforeUnmount(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <div ref="root">
    <span class="sr-only" role="status" aria-live="polite" aria-atomic="true" data-testid="bulk-live">{{ announcement }}</span>
    <div v-if="count > 0" class="bg-brand-soft text-brand-strong mx-3 mb-3 flex flex-wrap items-center gap-2 rounded-2xl px-4 py-2 text-sm" role="region" :aria-label="t('common.bulk.label')" data-testid="bulk-bar">
      <span class="font-medium" data-testid="bulk-count">{{ t("common.bulk.selected", { n: count }, count) }}</span>
      <Button v-if="offer" type="button" variant="outline" size="sm" :disabled="loading" :aria-busy="loading || undefined" data-testid="bulk-select-all" @click="emit('select-all')">
        <Loader v-if="loading" class="size-3.5 animate-spin" aria-hidden="true" />{{ more ? t("common.bulk.selectAllMore") : t("common.bulk.selectAll", { n: total ?? count }) }}
      </Button>
      <div class="flex flex-wrap items-center gap-2"><slot /></div>
      <Button type="button" variant="ghost" size="sm" class="ms-auto" aria-keyshortcuts="Escape" data-testid="bulk-clear" @click="clear"><X class="size-4" aria-hidden="true" />{{ t("common.bulk.clear") }}</Button>
    </div>
  </div>
</template>
