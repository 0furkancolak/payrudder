<script setup lang="ts" generic="M extends boolean = false">
import { Check, ChevronDown, X } from "@lucide/vue";
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from "reka-ui";
import { computed, ref } from "vue";
import { cn } from "@/shared/lib/utils";

/**
 * A filter of a table as a chip: its name, and the chosen option once there is one (the chip then turns brand-coloured and has a cross to clear it).
 * It opens a short list. With `multiple` the model is the list of chosen values (several may be on at once: the chip shows the first and how many more,
 * the list stays open to tick more), empty for "no filter"; without it the model is the one chosen value, empty for "no filter". Either way it stands in
 * for a select box wherever one filtered a list. An option whose value is empty is the "any" entry: it carries the words for it and clears the filter.
 */
const props = defineProps<{ label: string; options: readonly { value: string; label?: string }[]; multiple?: M }>();
const model = defineModel<M extends true ? string[] : string>();

/** `multiple` as a bare attribute reaches a generic prop as an empty string: anything but absent or false is on. */
const many = computed(() => props.multiple !== undefined && (props.multiple as unknown) !== false);
const open = ref(false);
const values = computed<string[]>(() => {
  const v = model.value as string | string[] | undefined;
  return (Array.isArray(v) ? v : v ? [v] : []).filter((x) => x !== "");
});
const text = (o: { value: string; label?: string }) => o.label ?? o.value;
const labelOf = (v: string) => {
  const o = props.options.find((x) => x.value === v);
  return o ? text(o) : v;
};
const first = computed(() => (values.value[0] === undefined ? undefined : labelOf(values.value[0])));
const rest = computed(() => values.value.length - 1);
const anyLabel = computed(() => props.options.find((x) => x.value === "")?.label ?? "—");

function pick(value: string) {
  if (!many.value) {
    model.value = value as never;
    open.value = false;
    return;
  }
  if (value === "") model.value = [] as never;
  else model.value = (values.value.includes(value) ? values.value.filter((v) => v !== value) : [...values.value, value]) as never;
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
              :aria-selected="o.value === '' ? values.length === 0 : values.includes(o.value)"
              class="hover:bg-accent flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 text-start text-sm"
              @click="pick(o.value)"
            >
              <span :class="o.value === '' ? 'text-muted-foreground' : ''">{{ o.value === "" ? anyLabel : text(o) }}</span>
              <Check v-if="o.value === '' ? values.length === 0 : values.includes(o.value)" class="size-4" aria-hidden="true" />
            </button>
          </li>
        </ul>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
  <button v-if="first !== undefined" type="button" class="text-muted-foreground hover:text-foreground -ms-1 rounded-full p-1" :aria-label="`${label}: ×`" @click="clear"><X class="size-3.5" aria-hidden="true" /></button>
</template>
