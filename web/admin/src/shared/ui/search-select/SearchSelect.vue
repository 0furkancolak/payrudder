<script setup lang="ts">
import { Check, ChevronsUpDown } from "@lucide/vue";
import { computed, nextTick, ref, useId, watch } from "vue";
import { cn } from "@/shared/lib/utils";
import { filterOptions, resolveTyped, type CustomOption } from "./filter";

/**
 * A select with a search box: the ARIA combobox pattern on a text input and a listbox. A click, typing or an arrow key opens the list (focus alone does not,
 * so a sheet that focuses its first field opens without a list over the form). Typing narrows the list (case and accents ignored, every word must
 * match the label, the hint or the value), the arrow keys move, Enter picks, Escape closes. Used for the currencies, the time zones and the places of an
 * address, where a plain list of hundreds is no use. Without `custom` the value is always one of the options. With it, what was typed is offered too (a
 * town the list does not have), leaving the field takes the text, and a stored value that is no option still shows.
 */
export interface SearchOption {
  value: string;
  /** The main text, e.g. "USD · US Dollar". */
  label: string;
  /** What the closed field shows instead of the label (e.g. just "USD"), to keep it short in a narrow form. */
  short?: string;
  /** Shown at the end of the row, e.g. "$" or "UTC+03:00". */
  hint?: string;
  /** More words the search matches, e.g. the English name of a currency shown in another language. */
  keywords?: string;
}

const props = defineProps<{
  options: SearchOption[];
  placeholder?: string;
  emptyText?: string;
  id?: string;
  ariaLabel?: string;
  disabled?: boolean;
  invalid?: boolean;
  class?: string;
  /** Turns the typed text into an option of its own (or null when it is not acceptable): free text next to the list. */
  custom?: CustomOption;
}>();
const model = defineModel<string>({ required: true });

const uid = useId();
const listId = `${uid}-list`;
const open = ref(false);
const query = ref("");
const active = ref(0);
const input = ref<HTMLInputElement | null>(null);
const list = ref<HTMLUListElement | null>(null);

/** Whether the person typed since the list opened: only then does leaving the field choose what was typed. */
const typed = ref(false);

const selected = computed(() => props.options.find((o) => o.value === model.value));
const closedText = computed(() => (selected.value ? (selected.value.short ?? selected.value.label) : props.custom ? model.value : ""));
const shown = computed<SearchOption[]>(() => filterOptions(props.options, query.value, props.custom));

watch(shown, () => {
  active.value = Math.max(0, Math.min(active.value, shown.value.length - 1));
});

async function show() {
  if (props.disabled || open.value) return;
  open.value = true;
  query.value = "";
  typed.value = false;
  const at = shown.value.findIndex((o) => o.value === model.value);
  active.value = at < 0 ? 0 : at;
  await nextTick();
  scrollToActive();
}

function hide() {
  open.value = false;
  query.value = "";
  typed.value = false;
}

/** Leaving the field after typing: the option the text names, or the text itself where free text is allowed (empty text clears it). */
function leave() {
  if (open.value && typed.value) {
    const v = resolveTyped(props.options, query.value, props.custom);
    if (v !== undefined && (props.custom || v !== "")) model.value = v;
  }
  hide();
}

function pick(o: SearchOption | undefined) {
  if (!o) return;
  model.value = o.value;
  hide();
  input.value?.focus();
}

function scrollToActive() {
  list.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: "nearest" });
}

function move(by: number) {
  if (shown.value.length === 0) return;
  active.value = (active.value + by + shown.value.length) % shown.value.length;
  void nextTick(scrollToActive);
}

function onKey(e: KeyboardEvent) {
  switch (e.key) {
    case "ArrowDown":
      e.preventDefault();
      if (!open.value) void show();
      else move(1);
      break;
    case "ArrowUp":
      e.preventDefault();
      if (!open.value) void show();
      else move(-1);
      break;
    case "Home":
      if (open.value) {
        e.preventDefault();
        active.value = 0;
        scrollToActive();
      }
      break;
    case "End":
      if (open.value) {
        e.preventDefault();
        active.value = Math.max(0, shown.value.length - 1);
        scrollToActive();
      }
      break;
    case "Enter":
      if (open.value) {
        e.preventDefault();
        pick(shown.value[active.value]);
      }
      break;
    case "Escape":
      if (open.value) {
        e.preventDefault();
        e.stopPropagation();
        hide();
      }
      break;
    case "Tab":
      leave();
      break;
  }
}

function onInput(e: Event) {
  query.value = (e.target as HTMLInputElement).value;
  open.value = true;
  typed.value = true;
  active.value = 0;
}
</script>

<template>
  <div :class="cn('relative', props.class)">
    <input
      :id="id"
      ref="input"
      type="text"
      role="combobox"
      autocomplete="off"
      autocapitalize="off"
      spellcheck="false"
      aria-autocomplete="list"
      :aria-label="ariaLabel"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-activedescendant="open && shown[active] ? `${uid}-opt-${active}` : undefined"
      :aria-invalid="invalid || undefined"
      :disabled="disabled"
      :placeholder="open ? (selected?.label ?? (props.custom && model ? model : placeholder)) : placeholder"
      :value="open ? query : closedText"
      class="border-input bg-card focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:border-destructive h-10 w-full min-w-0 truncate rounded-xl border ps-4 pe-9 text-base outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
      @click="show"
      @input="onInput"
      @keydown="onKey"
      @blur="leave"
    />
    <ChevronsUpDown class="text-muted-foreground pointer-events-none absolute end-3 top-1/2 size-4 -translate-y-1/2" aria-hidden="true" />
    <ul
      v-show="open"
      :id="listId"
      ref="list"
      role="listbox"
      class="border-border/70 bg-popover text-popover-foreground shadow-pop absolute inset-x-0 top-full z-50 mt-1.5 max-h-64 overflow-y-auto overscroll-contain rounded-2xl border p-1.5"
    >
      <li
        v-for="(o, i) in shown"
        :id="`${uid}-opt-${i}`"
        :key="`${i}:${o.value}`"
        role="option"
        :data-index="i"
        :aria-selected="o.value === model"
        :class="cn('flex cursor-pointer items-center gap-2 rounded-xl px-3 py-2 text-sm', i === active ? 'bg-accent text-accent-foreground' : '')"
        @mousedown.prevent
        @mousemove="active = i"
        @click="pick(o)"
      >
        <Check :class="cn('size-4 shrink-0', o.value === model ? 'opacity-100' : 'opacity-0')" aria-hidden="true" />
        <span class="min-w-0 flex-1 truncate">{{ o.label }}</span>
        <span v-if="o.hint" class="text-muted-foreground shrink-0 text-xs tabular-nums">{{ o.hint }}</span>
      </li>
      <li v-if="shown.length === 0" class="text-muted-foreground px-3 py-2 text-sm" role="presentation">{{ emptyText ?? "—" }}</li>
    </ul>
  </div>
</template>
