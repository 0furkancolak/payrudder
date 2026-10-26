<script setup lang="ts">
import { Check, ChevronDown, X } from "@lucide/vue";
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from "reka-ui";
import { computed } from "vue";
import { cn } from "@/shared/lib/utils";

/**
 * A filter of a table as a chip: its name, and the chosen option once there is one (the chip then turns brand-coloured and has a cross to clear it).
 * It opens a short list. The model is the chosen value, empty for "no filter", so it stands in for a select box wherever one filtered a list.
 */
const props = defineProps<{ label: string; options: readonly { value: string; label?: string }[] }>();
const model = defineModel<string>({ default: "" });
const chosen = computed(() => (model.value === "" ? undefined : props.options.find((o) => o.value === model.value)));
const text = (o: { value: string; label?: string }) => o.label ?? o.value;
</script>

<template>
  <PopoverRoot>
    <PopoverTrigger as-child>
      <button
        type="button"
        :data-active="chosen ? '' : undefined"
        :aria-label="label"
        :class="cn('inline-flex h-10 items-center gap-2 rounded-xl border px-3.5 text-sm font-medium transition-colors', chosen ? 'bg-brand-soft text-brand-strong border-transparent' : 'bg-card text-foreground border-border/70 hover:bg-muted')"
      >
        <span :class="chosen ? 'opacity-70' : ''">{{ label }}</span>
        <span v-if="chosen" class="max-w-32 truncate font-semibold">{{ text(chosen) }}</span>
        <ChevronDown v-if="!chosen" class="text-muted-foreground size-4" aria-hidden="true" />
      </button>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent align="start" :side-offset="6" class="bg-popover text-popover-foreground shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 z-50 max-h-72 min-w-44 overflow-y-auto rounded-2xl border p-1.5">
        <ul role="listbox" :aria-label="label" class="flex flex-col gap-0.5">
          <li v-for="o in [{ value: '', label: undefined }, ...options.filter((x) => x.value !== '')]" :key="o.value">
            <button type="button" role="option" :aria-selected="o.value === model" class="hover:bg-accent flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 text-start text-sm" @click="model = o.value">
              <span :class="o.value === '' ? 'text-muted-foreground' : ''">{{ o.value === "" ? (options.find((x) => x.value === "")?.label ?? "—") : text(o) }}</span>
              <Check v-if="o.value === model" class="size-4" aria-hidden="true" />
            </button>
          </li>
        </ul>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
  <button v-if="chosen" type="button" class="text-muted-foreground hover:text-foreground -ms-1 rounded-full p-1" :aria-label="`${label}: ×`" @click="model = ''"><X class="size-3.5" aria-hidden="true" /></button>
</template>
