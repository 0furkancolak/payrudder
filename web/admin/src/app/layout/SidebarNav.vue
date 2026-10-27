<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";
import type { NavItem, NavSection } from "./nav";

/**
 * The list of sections and links, shared by the desktop sidebar and the phone drawer. `collapsed` folds it to an icon rail: the labels stay in the DOM for
 * assistive technology and become a title (tooltip). The current item is the one the route says is current, and carries aria-current="page".
 */
const props = defineProps<{ sections: NavSection[]; current: NavItem | undefined; collapsed?: boolean; label: string }>();
const emit = defineEmits<{ navigate: [] }>();
const { t } = useI18n();

const isCurrent = (it: NavItem) => props.current?.key === it.key && props.current?.to === it.to;

function row(it: NavItem): string {
  const active = props.current?.to === it.to && props.current?.key === it.key;
  return cn(
    "group relative flex items-center gap-3 rounded-2xl text-sm font-medium transition-colors min-h-11 md:min-h-9",
    props.collapsed ? "justify-center px-0" : "px-3.5",
    active ? "bg-rail-accent text-rail-accent-foreground" : "text-rail-foreground hover:bg-rail-accent hover:text-rail-accent-foreground",
  );
}
</script>

<template>
  <nav :aria-label="label" class="flex flex-col gap-4" data-testid="sidebar-nav">
    <section v-for="s in sections" :key="s.key" :aria-labelledby="`nav-${s.key}`" :data-section="s.key" :class="s.key === 'account' || s.key === 'platform' ? 'border-rail-border border-t pt-3 first:border-t-0 first:pt-0' : undefined">
      <h2 :id="`nav-${s.key}`" :class="collapsed ? 'sr-only' : 'text-rail-muted mb-1.5 px-3.5 text-[0.7rem] font-semibold tracking-wider uppercase'">{{ t(`shell.sections.${s.key}`) }}</h2>
      <ul class="flex flex-col gap-0.5">
        <li v-for="it in s.items" :key="it.key">
          <Link
            :to="it.to"
            :search="(it.search ?? {}) as never"
            :class="row(it)"
            :aria-current="isCurrent(it) ? 'page' : undefined"
            :title="collapsed ? t(`shell.items.${it.label}`) : undefined"
            :data-nav="it.key"
            @click="emit('navigate')"
          >
            <span v-if="isCurrent(it)" class="bg-rail-primary absolute inset-y-2 start-0 w-1 rounded-full" aria-hidden="true" />
            <component :is="it.icon" class="size-[1.15rem] shrink-0" aria-hidden="true" />
            <span :class="collapsed ? 'sr-only' : 'truncate'">{{ t(`shell.items.${it.label}`) }}</span>
          </Link>
        </li>
      </ul>
    </section>
  </nav>
</template>
