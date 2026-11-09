<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronDown } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useMyUnread, useSupportReport } from "@/features/support/api";
import { foldedSections, toggleSection } from "@/shared/lib/prefs";
import { isOperator } from "@/shared/lib/session";
import { cn } from "@/shared/lib/utils";
import type { NavItem, NavSection } from "./nav";

/**
 * The list of sections and links, shared by the desktop sidebar and the phone drawer. `collapsed` folds it to an icon rail: the labels stay in the DOM for
 * assistive technology and become a title (tooltip). The current item is the one the route says is current, and carries aria-current="page". A group
 * folds under its heading (remembered per browser); the group of the current page always shows, and the icon rail shows every group.
 */
const props = defineProps<{ sections: NavSection[]; current: NavItem | undefined; collapsed?: boolean; label: string }>();
const emit = defineEmits<{ navigate: [] }>();
const { t } = useI18n();

/**
 * The count beside a link: the tickets the person has news on (the help page's link), or the tickets waiting for the operator (the inbox's). Each is asked only where
 * its link is in the list, and again every minute and after any change in the help desk; a count that cannot be had shows nothing.
 */
const has = (key: string) => props.sections.some((s) => s.items.some((it) => it.key === key));
const unread = useMyUnread(() => has("support"));
const waiting = useSupportReport(() => has("supportInbox") && isOperator.value);
const badge = (key: string): number => (key === "support" ? (unread.data.value ?? 0) : key === "supportInbox" ? (waiting.data.value?.waiting ?? 0) : 0);

const isCurrent = (it: NavItem) => props.current?.key === it.key && props.current?.to === it.to;
const folded = (s: NavSection) => !props.collapsed && foldedSections.value.includes(s.key) && !s.items.some(isCurrent);

function row(it: NavItem): string {
  const active = props.current?.to === it.to && props.current?.key === it.key;
  return cn(
    "group relative flex items-center gap-3 rounded-lg text-sm font-medium transition-colors min-h-11 md:min-h-8",
    props.collapsed ? "justify-center px-0" : "px-3",
    active ? "bg-rail-accent text-rail-accent-foreground" : "text-rail-foreground hover:bg-rail-accent hover:text-rail-accent-foreground",
  );
}
</script>

<template>
  <nav :aria-label="label" class="flex flex-col gap-3" data-testid="sidebar-nav">
    <section v-for="s in sections" :key="s.key" :aria-labelledby="`nav-${s.key}`" :data-section="s.key" :class="s.key === 'account' || s.key === 'platform' ? 'border-rail-border border-t pt-3 first:border-t-0 first:pt-0' : undefined">
      <h2 :id="`nav-${s.key}`" :class="collapsed ? 'sr-only' : 'mb-0.5'">
        <template v-if="collapsed">{{ t(`shell.sections.${s.key}`) }}</template>
        <button
          v-else
          type="button"
          class="text-rail-muted hover:text-rail-accent-foreground flex w-full items-center gap-1 rounded-md px-3 py-1 text-start text-xs font-medium transition-colors"
          :aria-expanded="!folded(s)"
          :aria-controls="`nav-list-${s.key}`"
          :data-fold="s.key"
          @click="toggleSection(s.key)"
        >
          <span class="flex-1">{{ t(`shell.sections.${s.key}`) }}</span>
          <ChevronDown :class="['size-3.5 shrink-0 transition-transform', folded(s) && '-rotate-90 rtl:rotate-90']" aria-hidden="true" />
        </button>
      </h2>
      <ul v-show="!folded(s)" :id="`nav-list-${s.key}`" class="flex flex-col gap-px">
        <li v-for="it in s.items" :key="it.key">
          <Link
            :to="it.to"
            :search="(it.search ?? {}) as never"
            :class="row(it)"
            :aria-current="isCurrent(it) ? 'page' : undefined"
            :title="t(`shell.items.${it.label}`)"
            :data-nav="it.key"
            @click="emit('navigate')"
          >
            <component :is="it.icon" class="size-[1.15rem] shrink-0" aria-hidden="true" />
            <span :class="collapsed ? 'sr-only' : 'truncate'">{{ t(`shell.items.${it.label}`) }}</span>
            <span
              v-if="badge(it.key) > 0"
              :class="collapsed ? 'bg-rail-primary absolute end-1.5 top-1.5 size-2 rounded-full' : 'bg-rail-primary text-rail ms-auto min-w-5 rounded-full px-1.5 text-center text-[0.7rem] leading-5 font-semibold tabular-nums'"
              :data-testid="`nav-badge-${it.key}`"
              :title="t(it.key === 'support' ? 'support.badge.unread' : 'support.badge.waiting', { n: badge(it.key) })"
              ><span :class="collapsed ? 'sr-only' : ''">{{ badge(it.key) }}</span></span
            >
          </Link>
        </li>
      </ul>
    </section>
  </nav>
</template>
