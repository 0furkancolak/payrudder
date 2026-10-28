<script setup lang="ts" generic="M extends boolean = false">
import { Check, ChevronsUpDown, Loader2, X } from "@lucide/vue";
import { computed, nextTick, onBeforeUnmount, reactive, ref, useId, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { fold } from "@/shared/lib/intl";
import { useAsyncOptions } from "@/shared/lib/useAsyncOptions";
import { cn } from "@/shared/lib/utils";
import { filterOptions, resolveTyped, type ComboboxOption, type CustomOption } from "./filter";

/**
 * The console's one list-with-a-search-box: the ARIA combobox pattern on a text input and a listbox, looking like the region field of an address.
 *
 * - One value (`v-model` a string, the closed field shows the chosen label) or several (`multiple`, `v-model` a string[]: the choices are chips in the
 *   field, Backspace removes the last, the list stays open to pick more).
 * - Searchable: typing narrows the list (case and accents ignored, every word must match the label, the hint, the value or the keywords). The list shows
 *   the first `limit` (10) options until something is typed, and says how many more there are.
 * - Creatable: with `creatable` (or `custom`, which turns the typed text into an option of its own or refuses it) what was typed is offered last as
 *   "Create “x”"; in one-value mode leaving the field takes the text, and a stored value that is no option still shows.
 * - Static list (`options`) or API list (`load(query, signal)`): the loader is asked when the list opens and, after `debounce` ms of quiet, for every
 *   change of the text; a spinner and placeholder rows show while it is asked, an answer older than the last question is dropped (and its request
 *   aborted through the signal), and a failure is shown in the list with a retry.
 * - Keyboard and screen reader: Arrow keys, Home, End, Enter, Escape; `aria-activedescendant`, `aria-multiselectable`, a polite status line with the
 *   number of results, the loading and the failure.
 *
 * A click, typing or an arrow key opens the list (focus alone does not, so a sheet that focuses its first field opens without a list over the form).
 */
const props = withDefaults(
  defineProps<{
    multiple?: M;
    /** The options of a static list. A `load`ed list needs none; the labels of values already chosen come from here or from earlier answers. */
    options?: ComboboxOption[];
    /** Asks an API for the options matching the text. Throwing (or rejecting) shows an error in the list. */
    load?: (query: string, signal: AbortSignal) => Promise<ComboboxOption[]>;
    /** Turns the typed text into an option of its own, or null when it is not acceptable: free text next to the list. */
    custom?: CustomOption;
    /** Offers the typed text as "Create “x”" (a `custom` of the caller's own wins). */
    creatable?: boolean;
    /** How many options an untouched list shows. */
    limit?: number;
    /** Quiet milliseconds before a typed text is sent to `load`. */
    debounce?: number;
    placeholder?: string;
    emptyText?: string;
    id?: string;
    ariaLabel?: string;
    disabled?: boolean;
    invalid?: boolean;
    /** The form cannot be sent while no value is chosen. */
    required?: boolean;
    class?: string;
  }>(),
  { limit: 10, debounce: 250 },
);
/** `multiple` as a bare attribute reaches a generic prop as an empty string: anything but absent or false is on. */
const many = computed(() => props.multiple !== undefined && (props.multiple as unknown) !== false);
const model = defineModel<M extends true ? string[] : string>({ required: true });

const { t } = useI18n();
const errorText = useErrorText();
const uid = useId();
const listId = `${uid}-list`;
/** How many matches a typed text may show (the untouched list shows `limit`). */
const MATCHES = 50;

const open = ref(false);
const query = ref("");
const active = ref(0);
const input = ref<HTMLInputElement | null>(null);
const list = ref<HTMLUListElement | null>(null);
/** Whether the person typed since the list opened: only then does leaving the one-value field choose what was typed. */
const typed = ref(false);

/** Every option seen (the static list and every answer of the loader), so a chosen value keeps its label while the list shows other things. */
const known = reactive(new Map<string, ComboboxOption>());
watch(
  () => props.options,
  (list) => list?.forEach((o) => known.set(o.value, o)),
  { immediate: true },
);

const chosen = computed<string[]>(() => (many.value ? ((model.value as string[]) ?? []) : (model.value as string) !== "" || known.has("") ? [model.value as string] : []));
const isChosen = (v: string) => chosen.value.includes(v);
const labelOf = (v: string) => known.get(v)?.short ?? known.get(v)?.label ?? v;

/** The one-value field when closed: the label of the choice (its short form), or the stored text itself where free text is allowed. */
const closedText = computed(() => {
  if (many.value) return "";
  const v = model.value as string;
  const o = known.get(v);
  return o ? (o.short ?? o.label) : v;
});

const makeCustom = computed<CustomOption | undefined>(() => props.custom ?? (props.creatable ? (q) => (q.trim() === "" ? null : { value: q.trim(), label: t("combobox.create", { value: q.trim() }) }) : undefined));

// The loader's side.
const asking = useAsyncOptions<ComboboxOption>(() => props.load, () => props.debounce);
const { answer, status, failure } = asking;
watch(answer, (got) => got.forEach((o) => known.set(o.value, o)));
onBeforeUnmount(asking.stop);

/** The typed text as an option, unless an option already is it. */
const typedOption = computed<ComboboxOption | null>(() => {
  const custom = makeCustom.value;
  if (!custom || query.value.trim() === "") return null;
  const o = custom(query.value);
  if (!o) return null;
  const base = props.load ? answer.value : (props.options ?? []);
  const want = fold(o.value);
  return base.some((x) => fold(x.value) === want || fold(x.label) === want) ? null : o;
});

/** What the list shows: the matches (the first `limit` while nothing is typed), then the typed text as a new option. */
const matches = computed<ComboboxOption[]>(() => (props.load ? answer.value : filterOptions(props.options ?? [], query.value)));
const untouched = computed(() => query.value.trim() === "");
const cap = computed(() => (untouched.value ? props.limit : MATCHES));
// The empty choice ("No country") matches its own words, but is never what a search is for: once something is typed it comes last, after the typed text.
const blanks = computed(() => (untouched.value ? [] : matches.value.filter((o) => o.value === "")));
const named = computed(() => (untouched.value ? matches.value : matches.value.filter((o) => o.value !== "")));
const shown = computed<ComboboxOption[]>(() => [...named.value.slice(0, cap.value), ...(typedOption.value ? [typedOption.value] : []), ...blanks.value]);
const hidden = computed(() => Math.max(0, named.value.length - cap.value));
const loadingFirst = computed(() => status.value === "loading" && answer.value.length === 0);
const empty = computed(() => status.value === "idle" && shown.value.length === 0);

const announce = computed(() => {
  if (!open.value) return "";
  if (status.value === "loading") return t("combobox.loading");
  if (status.value === "error") return errorText(failure.value);
  return t("combobox.results", { n: shown.value.length });
});

watch(shown, () => {
  active.value = Math.max(0, Math.min(active.value, shown.value.length - 1));
});

async function show() {
  if (props.disabled || open.value) return;
  open.value = true;
  query.value = "";
  typed.value = false;
  if (props.load) {
    asking.reset();
    asking.ask("", true);
  }
  const at = shown.value.findIndex((o) => isChosen(o.value));
  active.value = many.value || at < 0 ? 0 : at;
  await nextTick();
  scrollToActive();
  revealList();
}

function hide() {
  open.value = false;
  query.value = "";
  typed.value = false;
  asking.stop();
}

/** Leaving a one-value field after typing: the option the text names, or the text itself where free text is allowed (empty text clears it). */
function leave() {
  if (open.value && typed.value && !many.value) {
    const v = resolveTyped([...known.values(), ...matches.value], query.value, makeCustom.value);
    if (v !== undefined && (makeCustom.value || v !== "")) {
      if (!known.has(v)) known.set(v, { value: v, label: v });
      model.value = v as never;
    }
  }
  hide();
}

function pick(o: ComboboxOption | undefined) {
  if (!o) return;
  if (!known.has(o.value) && o === typedOption.value) known.set(o.value, { value: o.value, label: o.short ?? o.value });
  else known.set(o.value, known.get(o.value) ?? o);
  if (many.value) {
    const now = (model.value as string[]) ?? [];
    model.value = (now.includes(o.value) ? now.filter((v) => v !== o.value) : [...now, o.value]) as never;
    query.value = "";
    typed.value = false;
    if (props.load) asking.ask("", true);
    input.value?.focus();
    return;
  }
  model.value = o.value as never;
  hide();
  input.value?.focus();
}

function remove(v: string) {
  model.value = ((model.value as string[]) ?? []).filter((x) => x !== v) as never;
  input.value?.focus();
}

/**
 * Brings the active option into view by scrolling the list itself. Not `scrollIntoView`: that scrolls every scrollable ancestor too, so in a sheet it moved
 * the sheet's body (and the page behind it) while the list was being walked with the arrow keys.
 */
function scrollToActive() {
  const box = list.value;
  const item = box?.querySelector<HTMLElement>(`[data-index="${active.value}"]`);
  if (!box || !item) return;
  const top = item.offsetTop - box.offsetTop;
  if (top < box.scrollTop) box.scrollTop = top;
  else if (top + item.offsetHeight > box.scrollTop + box.clientHeight) box.scrollTop = top + item.offsetHeight - box.clientHeight;
}

/** A list that opens below the fold of a sheet's scrolling body is brought into view once, by scrolling the nearest scrolling ancestor and nothing above it. */
function revealList() {
  const box = list.value;
  if (!box) return;
  let parent = box.parentElement;
  while (parent && !(parent.scrollHeight > parent.clientHeight && /(auto|scroll)/.test(getComputedStyle(parent).overflowY))) parent = parent.parentElement;
  if (!parent) return;
  const room = parent.getBoundingClientRect().bottom - box.getBoundingClientRect().bottom;
  if (room < 0) parent.scrollTop += -room + 8;
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
    case "Backspace":
      if (many.value && query.value === "" && chosen.value.length > 0) remove(chosen.value[chosen.value.length - 1] ?? "");
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
  asking.ask(query.value);
}

function onFieldClick(e: MouseEvent) {
  if ((e.target as HTMLElement).closest("[data-chip-remove]")) return;
  input.value?.focus();
  void show();
}
</script>

<template>
  <div :class="cn('relative', props.class)">
    <div :class="cn('field flex min-h-10 w-full min-w-0 items-center gap-1.5 rounded-xl ps-4 pe-9 text-base md:text-sm', many && chosen.length > 0 ? 'flex-wrap py-1.5 ps-2.5' : '', disabled ? 'cursor-not-allowed opacity-50' : '')" @click="onFieldClick">
      <span v-for="v in many ? chosen : []" :key="v" class="bg-secondary text-secondary-foreground inline-flex max-w-full items-center gap-1 rounded-full py-0.5 ps-2.5 pe-1 text-xs font-medium" data-testid="combobox-chip">
        <span class="truncate">{{ labelOf(v) }}</span>
        <button type="button" data-chip-remove class="hover:bg-foreground/10 rounded-full p-0.5" :disabled="disabled" :aria-label="t('combobox.remove', { label: labelOf(v) })" @mousedown.prevent @click.stop="remove(v)">
          <X class="size-3" aria-hidden="true" />
        </button>
      </span>
      <input
        :id="id"
        ref="input"
        type="text"
        role="combobox"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        aria-autocomplete="list"
        aria-haspopup="listbox"
        :aria-label="ariaLabel"
        :aria-expanded="open"
        :aria-controls="listId"
        :aria-busy="status === 'loading' || undefined"
        :aria-activedescendant="open && shown[active] ? `${uid}-opt-${active}` : undefined"
        :aria-invalid="invalid || undefined"
        :disabled="disabled"
        :required="required && chosen.length === 0"
        :placeholder="many ? (chosen.length === 0 ? placeholder : undefined) : open ? (known.get(model as string)?.label ?? (makeCustom && model ? (model as string) : placeholder)) : placeholder"
        :value="open ? query : closedText"
        :class="cn('placeholder:text-muted-foreground h-full min-h-8 truncate bg-transparent outline-none disabled:cursor-not-allowed', many ? 'min-w-24 flex-1' : 'w-full min-w-0')"
        @input="onInput"
        @keydown="onKey"
        @blur="leave"
      />
    </div>
    <Loader2 v-if="status === 'loading'" class="text-muted-foreground pointer-events-none absolute end-3 top-5 size-4 -translate-y-1/2 animate-spin" aria-hidden="true" data-testid="combobox-loading" />
    <ChevronsUpDown v-else class="text-muted-foreground pointer-events-none absolute end-3 top-5 size-4 -translate-y-1/2" aria-hidden="true" />
    <ul
      v-show="open"
      :id="listId"
      ref="list"
      role="listbox"
      :aria-multiselectable="many || undefined"
      :aria-busy="status === 'loading' || undefined"
      :class="cn('border-border/70 bg-popover text-popover-foreground shadow-pop absolute inset-x-0 top-full z-50 mt-1.5 max-h-64 overflow-y-auto overscroll-contain rounded-2xl border p-1.5', status === 'loading' && !loadingFirst ? 'opacity-70' : '')"
    >
      <template v-if="loadingFirst">
        <li v-for="n in 3" :key="`s${n}`" role="presentation" class="px-3 py-2" data-testid="combobox-skeleton"><span class="bg-foreground/[0.07] block h-4 animate-pulse rounded-md" :style="{ width: `${90 - n * 18}%` }" /></li>
      </template>
      <li v-else-if="status === 'error'" role="presentation" class="flex items-center justify-between gap-3 px-3 py-2 text-sm" data-testid="combobox-error">
        <span role="alert" class="text-destructive min-w-0">{{ errorText(failure) }}</span>
        <button type="button" class="hover:bg-accent shrink-0 rounded-lg px-2 py-1 text-xs font-medium" @mousedown.prevent @click="asking.again()">{{ t("combobox.retry") }}</button>
      </li>
      <template v-else>
        <li
          v-for="(o, i) in shown"
          :id="`${uid}-opt-${i}`"
          :key="`${i}:${o.value}`"
          role="option"
          :data-index="i"
          :aria-selected="isChosen(o.value)"
          :class="cn('flex cursor-pointer items-center gap-2 rounded-xl px-3 py-2 text-sm', i === active ? 'bg-accent text-accent-foreground' : '')"
          @mousedown.prevent
          @mousemove="active = i"
          @click="pick(o)"
        >
          <Check :class="cn('size-4 shrink-0', isChosen(o.value) ? 'opacity-100' : 'opacity-0')" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate">{{ o.label }}</span>
          <span v-if="o.hint" class="text-muted-foreground shrink-0 text-xs tabular-nums">{{ o.hint }}</span>
        </li>
        <li v-if="empty" class="text-muted-foreground px-3 py-2 text-sm" role="presentation">{{ emptyText ?? t("picker.noMatch") }}</li>
        <li v-if="hidden > 0" class="text-muted-foreground px-3 py-2 text-xs" role="presentation" data-testid="combobox-more">{{ t("combobox.more", { n: hidden }) }}</li>
      </template>
    </ul>
    <p v-if="open" class="sr-only" role="status" aria-live="polite">{{ announce }}</p>
  </div>
</template>
