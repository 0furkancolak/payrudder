<script setup lang="ts">
import { CalendarDays } from "@lucide/vue";
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from "reka-ui";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { cn } from "@/shared/lib/utils";
import { rangeOf, type RangePreset } from "@/shared/lib/dateRange";

/**
 * A date range for a list: presets on the left (today, the last 7 and 30 days, this and last month, this year) and two dates for anything else. The
 * model is `{ from, to }` as YYYY-MM-DD, both empty for "any time". The two dates are the browser's own date fields (a wheel on a phone, a calendar on a
 * desktop), so they work with touch and with the keyboard everywhere. The panel never leaves the screen: it flips to the side with room, slides along the
 * edge it would cross, and is never wider than the screen less a margin (on a 375 px phone the presets sit above the dates).
 */
export interface DateRange {
  from: string;
  to: string;
}
const model = defineModel<DateRange>({ default: () => ({ from: "", to: "" }) });
const { t } = useI18n();
const presets: RangePreset[] = ["today", "7d", "30d", "month", "lastMonth", "year"];
const open = ref(false);
const draft = ref<DateRange>({ ...model.value });
watch(model, (v) => (draft.value = { ...v }), { deep: true });

const active = computed(() => model.value.from !== "" || model.value.to !== "");
const label = computed(() => (active.value ? `${model.value.from || "…"} – ${model.value.to || "…"}` : t("dateRange.any")));
const valid = computed(() => draft.value.from === "" || draft.value.to === "" || draft.value.from <= draft.value.to);

function pick(p: RangePreset) {
  model.value = rangeOf(p, new Date());
  open.value = false;
}
function apply() {
  if (!valid.value) return;
  model.value = { ...draft.value };
  open.value = false;
}
function clear() {
  model.value = { from: "", to: "" };
  open.value = false;
}
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger as-child>
      <button type="button" :aria-label="t('dateRange.label')" :data-active="active ? '' : undefined" :class="cn('field inline-flex h-10 max-w-full items-center gap-2 rounded-xl px-3.5 text-sm font-medium', active ? '' : 'text-foreground hover:bg-muted')" data-testid="date-range">
        <CalendarDays class="size-4" aria-hidden="true" />
        <span class="tabular-nums">{{ label }}</span>
      </button>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="start"
        :side-offset="6"
        :collision-padding="12"
        avoid-collisions
        sticky="always"
        data-testid="date-range-panel"
        class="bg-popover text-popover-foreground shadow-pop data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 z-50 flex max-h-[min(var(--reka-popover-content-available-height),32rem)] w-[min(34rem,calc(100vw-1.5rem))] max-w-[calc(100vw-1.5rem)] flex-col gap-4 overflow-y-auto overscroll-contain rounded-2xl border p-4 sm:flex-row"
      >
        <ul class="flex shrink-0 flex-wrap gap-1.5 sm:w-40 sm:flex-col sm:gap-0.5">
          <li v-for="p in presets" :key="p">
            <button type="button" class="hover:bg-accent w-full rounded-xl px-3 py-1.5 text-start text-sm" :data-testid="`range-${p}`" @click="pick(p)">{{ t(`dateRange.presets.${p}`) }}</button>
          </li>
        </ul>
        <form class="flex flex-1 flex-col gap-3" @submit.prevent="apply">
          <label class="flex flex-col gap-1 text-xs font-medium">{{ t("dateRange.from") }}<Input v-model="draft.from" type="date" class="min-w-0" :max="draft.to || undefined" /></label>
          <label class="flex flex-col gap-1 text-xs font-medium">{{ t("dateRange.to") }}<Input v-model="draft.to" type="date" class="min-w-0" :min="draft.from || undefined" /></label>
          <div class="mt-auto flex justify-end gap-2">
            <Button type="button" variant="ghost" size="sm" @click="clear">{{ t("dateRange.clear") }}</Button>
            <Button type="submit" size="sm" :disabled="!valid">{{ t("dateRange.apply") }}</Button>
          </div>
        </form>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
