<script setup lang="ts">
import { nextTick, ref } from "vue";
import { useI18n } from "vue-i18n";
import { currencyInfo } from "@/shared/lib/intl";
import { nextTabIndex } from "./lib";

/**
 * One tab per currency, because money of different currencies is never added up: every figure below belongs to the chosen one. A tablist with the arrow
 * keys, Home and End; the active tab is the only one in the tab order.
 */
const props = defineProps<{ currencies: readonly string[]; active: string; defaultCurrency: string }>();
const emit = defineEmits<{ select: [string] }>();
const { t, locale } = useI18n();
const buttons = ref<HTMLButtonElement[]>([]);

function key(e: KeyboardEvent, i: number) {
  const to = nextTabIndex(e.key, i, props.currencies.length);
  if (to < 0) return;
  e.preventDefault();
  const c = props.currencies[to];
  if (c) emit("select", c);
  void nextTick(() => buttons.value[to]?.focus());
}
</script>

<template>
  <div v-if="currencies.length > 0" role="tablist" :aria-label="t('dash.currency.label')" class="flex flex-wrap gap-2" data-testid="currency-tabs">
    <button
      v-for="(c, i) in currencies"
      :key="c"
      :ref="(el) => (buttons[i] = el as HTMLButtonElement)"
      type="button"
      role="tab"
      :aria-selected="c === active"
      :tabindex="c === active ? 0 : -1"
      :data-testid="`currency-tab-${c}`"
      :title="c === defaultCurrency ? `${currencyInfo(c, locale).name} · ${t('dash.currency.ownHint')}` : currencyInfo(c, locale).name"
      :class="[
        'inline-flex h-10 items-center gap-2 rounded-xl border px-4 text-sm font-semibold transition-colors',
        c === active ? 'bg-primary text-primary-foreground border-primary shadow-sm' : 'bg-card text-muted-foreground hover:text-foreground border-border/70',
      ]"
      @click="emit('select', c)"
      @keydown="key($event, i)"
    >
      {{ c }}
      <span v-if="c === defaultCurrency" :class="['text-[0.65rem] font-medium tracking-wide uppercase', c === active ? 'opacity-80' : 'text-muted-foreground']">{{ t("dash.currency.own") }}</span>
    </button>
  </div>
</template>
