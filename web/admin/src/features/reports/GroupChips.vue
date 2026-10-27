<script setup lang="ts">
import { Check } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";

/** The "split by" choices of a report: any number of the groups can be on, each a chip that is a checkbox to a screen reader. */
const props = defineProps<{ groups: string[]; label: string }>();
const model = defineModel<string[]>({ required: true });
const { t } = useI18n();

const toggle = (g: string) => (model.value = model.value.includes(g) ? model.value.filter((x) => x !== g) : [...model.value, g]);
const chip = (on: boolean) =>
  cn(
    "inline-flex h-10 items-center gap-1.5 rounded-xl border px-3.5 text-sm font-medium transition-colors",
    on ? "bg-brand-soft text-brand-strong border-transparent" : "bg-card text-foreground border-border/70 hover:bg-muted",
  );
</script>

<template>
  <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="props.label">
    <button v-for="g in groups" :key="g" type="button" role="checkbox" :aria-checked="model.includes(g)" :aria-label="t(`billing.reports.by.${g}`)" :class="chip(model.includes(g))" @click="toggle(g)">
      <Check v-if="model.includes(g)" class="size-4" aria-hidden="true" />{{ t(`billing.reports.by.${g}`) }}
    </button>
  </div>
</template>
