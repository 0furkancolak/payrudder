<script setup lang="ts" generic="M extends boolean = false">
import { Check, ChevronDown, X } from "@lucide/vue";
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from "reka-ui";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";

/**
 * A filter of a table as a chip: its name, and the chosen option once there is one (the chip then turns brand-coloured and has a cross to clear it).
 * It opens a short list. With `multiple` the model is the list of chosen values (several may be on at once: the chip shows the first and how many more,
 * the list stays open to tick more), empty for "no filter"; without it the model is the one chosen value, empty for "no filter". Either way it stands in
 * for a select box wherever one filtered a list. An option whose value is empty is the "any" entry: it carries the words for it and clears the filter.
 *
 * Choosing in the list changes nothing yet: the choice is a draft, and the list (and the page behind it) follows it only when the person presses "Apply".
 * Closing the list any other way throws the draft away. The cross on the chip clears the filter at once.
 */
const props = defineProps<{ label: string; options: readonly { value: string; label?: string }[]; multiple?: M }>();
const model = defineModel<M extends true ? string[] : string>();
const { t } = useI18n();

/** `multiple` as a bare attribute reaches a generic prop as an empty string: anything but absent or false is on. */
const many = computed(() => props.multiple !== undefined && (props.multiple as unknown) !== false);
const open = ref(false);
const toList = (v: string | string[] | undefined) => (Array.isArray(v) ? v : v ? [v] : []).filter((x) => x !== "");
/** What is applied: the model. */
const values = computed<string[]>(() => toList(model.value as string | string[] | undefined));
/** What the open list shows: the draft, which starts as what is applied. */
const draft = ref<string[]>([]);
watch(open, (isOpen) => {
  if (isOpen) draft.value = [...values.value];
});
const changed = computed(() => draft.value.length !== values.value.length || draft.value.some((v) => !values.value.includes(v)));
const text = (o: { value: string; label?: string }) => o.label ?? o.value;
const labelOf = (v: string) => {
  const o = props.options.find((x) => x.value === v);
  return o ? text(o) : v;
};
const first = computed(() => (values.value[0] === undefined ? undefined : labelOf(values.value[0])));
const rest = computed(() => values.value.length - 1);
const anyLabel = computed(() => props.options.find((x) => x.value === "")?.label ?? "—");

function pick(value: string) {
  if (value === "") draft.value = [];
  else if (!many.value) draft.value = [value];
  else draft.value = draft.value.includes(value) ? draft.value.filter((v) => v !== value) : [...draft.value, value];
}
function apply() {
  model.value = (many.value ? [...draft.value] : (draft.value[0] ?? "")) as never;
  open.value = false;
}
function clear() {
  model.value = (many.value ? [] : "") as never;
}
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger as-child>
      <button
        type="button"
        :data-active="first !== undefined ? '' : undefined"
        :aria-label="label"
        :class="cn('field inline-flex h-10 items-center gap-2 rounded-xl px-3.5 text-sm font-medium', first === undefined ? 'text-foreground hover:bg-muted' : '')"
      >
        <span :class="first !== undefined ? 'opacity-70' : ''">{{ label }}</span>
        <span v-if="first !== undefined" class="max-w-32 truncate font-semibold">{{ first }}</span>
        <span v-if="rest > 0" class="bg-brand/25 rounded-full px-1.5 text-xs font-semibold tabular-nums" data-testid="filter-more">+{{ rest }}</span>
        <ChevronDown v-if="first === undefined" class="text-muted-foreground size-4" aria-hidden="true" />
      </button>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent align="start" :side-offset="6" :collision-padding="12" class="bg-popover text-popover-foreground shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 z-50 max-h-72 min-w-44 max-w-[calc(100vw-1.5rem)] overflow-y-auto overscroll-contain rounded-2xl border p-1.5">
        <ul role="listbox" :aria-label="label" :aria-multiselectable="many || undefined" class="flex flex-col gap-0.5">
          <li v-for="o in [{ value: '', label: undefined }, ...options.filter((x) => x.value !== '')]" :key="o.value">
            <button
              type="button"
              role="option"
              :aria-selected="o.value === '' ? draft.length === 0 : draft.includes(o.value)"
              class="hover:bg-accent flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 text-start text-sm"
              @click="pick(o.value)"
            >
              <span :class="o.value === '' ? 'text-muted-foreground' : ''">{{ o.value === "" ? anyLabel : text(o) }}</span>
              <Check v-if="o.value === '' ? draft.length === 0 : draft.includes(o.value)" class="size-4" aria-hidden="true" />
            </button>
          </li>
        </ul>
        <div class="mt-1.5 flex justify-end border-t pt-1.5">
          <button type="button" class="bg-primary text-primary-foreground hover:bg-primary/90 rounded-xl px-3 py-1.5 text-sm font-medium disabled:opacity-50" :disabled="!changed" data-testid="filter-apply" @click="apply">{{ t("common.applyFilter") }}</button>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
  <button v-if="first !== undefined" type="button" class="text-muted-foreground hover:text-foreground -ms-1 rounded-full p-1" :aria-label="`${label}: ×`" @click="clear"><X class="size-3.5" aria-hidden="true" /></button>
</template>
