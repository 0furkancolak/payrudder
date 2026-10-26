<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronDown } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { cn } from "@/shared/lib/utils";
import { foldedGroups, toggleGroup } from "@/shared/lib/prefs";
import { defaultEnv, type Environment } from "@/shared/lib/environment";
import type { NavItem, NavSection } from "./nav";

/** An app of the person's organisation, as the sidebar lists it. */
export interface SidebarApp {
  id?: string;
  name?: string;
  tenants?: { id?: string; environment?: string; status?: string }[];
}

/**
 * The list of sections and links, shared by the desktop sidebar and the phone drawer. `collapsed` folds it to an icon rail: the labels stay in the DOM for
 * assistive technology and become a title (tooltip). The current item is the one the route says is current, and carries aria-current="page".
 */
const props = defineProps<{ sections: NavSection[]; current: NavItem | undefined; collapsed?: boolean; label: string; apps?: SidebarApp[]; appId?: string; env?: Environment; nest?: boolean }>();
const emit = defineEmits<{ navigate: [] }>();
const { t } = useI18n();

/** The environment an app link opens: the one the person is looking at, else where the app was left. */
const envOfApp = (a: SidebarApp): Environment => props.env ?? defaultEnv(a as never);
const initial = (name?: string) => (name ?? "?").trim().charAt(0).toUpperCase() || "?";

/** An app's pages are drawn under it (its own group of groups) while the sidebar is open; the icon rail keeps the flat list. */
const nested = computed(() => !!props.nest && !!props.appId && !props.collapsed);
const isCurrent = (it: NavItem) => props.current?.key === it.key && props.current?.to === it.to;
/** A group is open unless the person folded it; the group of the current page never hides it. */
const groupOpen = (s: NavSection) => !foldedGroups.value.includes(s.key) || s.items.some(isCurrent);

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
    <section v-if="apps" aria-labelledby="nav-apps" data-testid="sidebar-apps">
      <h2 id="nav-apps" :class="collapsed ? 'sr-only' : 'text-rail-muted mb-1.5 px-3.5 text-[0.7rem] font-semibold tracking-wider uppercase'">{{ t("shell.sections.apps") }}</h2>
      <ul class="flex flex-col gap-0.5">
        <li v-for="a in apps" :key="a.id">
          <Link
            :to="'/app/apps/' + a.id"
            :search="{ env: envOfApp(a) } as never"
            :class="cn('group relative flex min-h-11 items-center gap-3 rounded-2xl text-sm font-medium transition-colors md:min-h-9', collapsed ? 'justify-center px-0' : 'px-3.5', a.id === appId ? 'bg-rail-accent text-rail-accent-foreground' : 'text-rail-foreground hover:bg-rail-accent hover:text-rail-accent-foreground')"
            :aria-current="a.id === appId && !nested ? 'true' : undefined"
            :aria-expanded="nested ? a.id === appId : undefined"
            :title="collapsed ? a.name : undefined"
            :data-app="a.id"
            @click="emit('navigate')"
          >
            <span class="bg-rail-primary/20 text-rail-primary flex size-[1.15rem] shrink-0 items-center justify-center rounded-md text-[0.65rem] font-semibold before:content-[attr(data-initial)]" :data-initial="initial(a.name)" aria-hidden="true" />
            <span :class="collapsed ? 'sr-only' : 'min-w-0 flex-1 truncate'">{{ a.name }}</span>
            <ChevronDown v-if="nested" :class="cn('text-rail-muted size-4 shrink-0 transition-transform', a.id !== appId && '-rotate-90 rtl:rotate-90')" aria-hidden="true" />
          </Link>

          <div v-if="nested && a.id === appId" class="border-rail-border ms-5 mt-1 mb-2 flex flex-col gap-2 border-s ps-2" data-testid="sidebar-app-pages">
            <section v-for="s in sections" :key="s.key" :aria-labelledby="`nav-${s.key}`">
              <h2 :id="`nav-${s.key}`" class="m-0">
                <button type="button" class="text-rail-muted hover:text-rail-foreground flex min-h-8 w-full items-center justify-between rounded-xl px-2.5 text-[0.7rem] font-semibold tracking-wider uppercase transition-colors" :aria-expanded="groupOpen(s)" @click="toggleGroup(s.key)">
                  {{ t(`shell.sections.${s.key}`) }}
                  <ChevronDown :class="cn('size-3.5 transition-transform', !groupOpen(s) && '-rotate-90 rtl:rotate-90')" aria-hidden="true" />
                </button>
              </h2>
              <ul v-show="groupOpen(s)" class="flex flex-col gap-0.5">
                <li v-for="it in s.items" :key="it.key">
                  <Link :to="it.to" :search="(it.search ?? {}) as never" :class="row(it)" :aria-current="isCurrent(it) ? 'page' : undefined" :data-nav="it.key" @click="emit('navigate')">
                    <span v-if="isCurrent(it)" class="bg-rail-primary absolute inset-y-2 -start-2.5 w-1 rounded-full" aria-hidden="true" />
                    <component :is="it.icon" class="size-[1.15rem] shrink-0" aria-hidden="true" />
                    <span class="truncate">{{ t(`shell.items.${it.label}`) }}</span>
                  </Link>
                </li>
              </ul>
            </section>
          </div>
        </li>
        <li v-if="apps.length === 0 && !collapsed" class="text-rail-muted px-3.5 py-1 text-xs">{{ t("shell.noApps") }}</li>
      </ul>
    </section>

    <template v-if="!nested">
      <section v-for="s in sections" :key="s.key" :aria-labelledby="`nav-${s.key}`">
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
    </template>
  </nav>
</template>
