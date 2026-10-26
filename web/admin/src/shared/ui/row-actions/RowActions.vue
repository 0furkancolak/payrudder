<script setup lang="ts">
import { MoreHorizontal } from "@lucide/vue";
import type { Component } from "vue";
import { useI18n } from "vue-i18n";
import { Button } from "@/shared/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";

/**
 * What can be done to a row. One action is a plain button; two or more fold into a "more" menu, so a row never carries a row of buttons. An action with
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
const shown = () => props.actions.filter((a) => !a.disabled || props.actions.length > 1);
</script>

<template>
  <div v-if="actions.length === 1" class="flex justify-end">
    <Button v-for="a in actions" :key="a.key" size="sm" variant="outline" :disabled="a.disabled" :data-testid="`row-action-${a.key}`" @click.stop="a.onSelect()">
      <component :is="a.icon" v-if="a.icon" class="size-4" aria-hidden="true" />{{ a.label }}
    </Button>
  </div>
  <div v-else-if="actions.length > 1" class="flex justify-end">
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button size="icon" variant="ghost" class="size-8 rounded-full" :aria-label="label ?? t('common.moreActions')" data-testid="row-actions" @click.stop>
          <MoreHorizontal class="size-4" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="min-w-44">
        <template v-for="a in shown()" :key="a.key">
          <DropdownMenuSeparator v-if="a.separatorBefore" />
          <DropdownMenuItem class="rounded-xl" :variant="a.destructive ? 'destructive' : 'default'" :disabled="a.disabled" :data-testid="`row-action-${a.key}`" @select="a.onSelect()">
            <component :is="a.icon" v-if="a.icon" class="size-4" aria-hidden="true" />{{ a.label }}
          </DropdownMenuItem>
        </template>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
