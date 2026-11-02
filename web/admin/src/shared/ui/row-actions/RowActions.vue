<script setup lang="ts">
import { MoreHorizontal } from "@lucide/vue";
import { useMediaQuery } from "@vueuse/core";
import { computed, ref, type Component } from "vue";
import { useI18n } from "vue-i18n";
import { openTabs, rememberOpener } from "@/shared/lib/sheets";
import { Button } from "@/shared/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";

/**
 * What can be done to a row. One action is a plain button; two or more fold into a "more" menu, so a row never carries a row of buttons. Where a table is shown as cards (below `lg`) a card has room: up to three actions stand in a row there. An action with
 * `href` is not offered here: a link is a cell, not an action. `separatorBefore` draws a line above an action (the destructive ones, last).
 */
export interface RowAction {
  key: string;
  label: string;
  icon?: Component;
  destructive?: boolean;
  disabled?: boolean;
  separatorBefore?: boolean;
  onSelect: () => void;
}
const props = defineProps<{ actions: RowAction[]; label?: string }>();
const { t } = useI18n();
/** In a card an action list that is short is shown in full; a long one still folds into the menu. */
const card = useMediaQuery("(max-width: 1023px)");
const inline = computed(() => card.value && props.actions.length > 1 && props.actions.length <= 3);
const shown = () => props.actions.filter((a) => !a.disabled || props.actions.length > 1);

// An action of the menu that opens a sheet: the sheet takes the focus (the menu must not hand it back to its button as it closes), and gives it back to that button when it closes.
const trigger = ref<{ $el: HTMLElement } | null>(null);
let sheetsBefore = 0;
function select(a: RowAction) {
  sheetsBefore = openTabs.value.length;
  rememberOpener(trigger.value?.$el ?? null);
  a.onSelect();
}
function keepFocusInSheet(e: Event) {
  if (openTabs.value.length > sheetsBefore) e.preventDefault();
}
</script>

<template>
  <div v-if="inline" class="flex flex-wrap justify-end gap-2">
    <Button
      v-for="a in actions"
      :key="a.key"
      size="sm"
      variant="outline"
      :class="a.destructive ? 'text-destructive' : ''"
      :disabled="a.disabled"
      :data-testid="`row-action-${a.key}`"
      @click.stop="a.onSelect()"
    >
      <component :is="a.icon" v-if="a.icon" class="size-4" aria-hidden="true" />{{ a.label }}
    </Button>
  </div>
  <div v-else-if="actions.length === 1" class="flex justify-end">
    <Button v-for="a in actions" :key="a.key" size="sm" variant="outline" :disabled="a.disabled" :data-testid="`row-action-${a.key}`" @click.stop="a.onSelect()">
      <component :is="a.icon" v-if="a.icon" class="size-4" aria-hidden="true" />{{ a.label }}
    </Button>
  </div>
  <div v-else-if="actions.length > 1" class="flex justify-end">
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button ref="trigger" size="icon" variant="ghost" class="size-8 rounded-full" :aria-label="label ?? t('common.moreActions')" data-testid="row-actions" @click.stop>
          <MoreHorizontal class="size-4" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="min-w-44" @close-auto-focus="keepFocusInSheet">
        <template v-for="a in shown()" :key="a.key">
          <DropdownMenuSeparator v-if="a.separatorBefore" />
          <DropdownMenuItem class="rounded-xl" :variant="a.destructive ? 'destructive' : 'default'" :disabled="a.disabled" :data-testid="`row-action-${a.key}`" @select="select(a)">
            <component :is="a.icon" v-if="a.icon" class="size-4" aria-hidden="true" />{{ a.label }}
          </DropdownMenuItem>
        </template>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
