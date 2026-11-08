<script setup lang="ts">
import { useNavigate } from "@tanstack/vue-router";
import { CornerDownLeft, Eye, Moon, Search, Sun } from "@lucide/vue";
import { usePreferredDark } from "@vueuse/core";
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from "reka-ui";
import { computed, ref, watch, type Component } from "vue";
import { useI18n } from "vue-i18n";
import { privacyMode, setPrivacyMode } from "@/shared/lib/prefs";
import { applyTheme, isDarkFor, theme } from "@/shared/lib/theme";
import type { NavSection } from "./nav";
import { matches } from "./palette";

/**
 * The command palette (⌘K / Ctrl+K, or the search button of the sidebar and the phone's tab bar): every page of the sidebar and a few actions, found by
 * typing. Arrow keys move, Enter opens, Escape closes. The pages are the sidebar's own (nav.ts), so the palette never offers a page the person cannot open.
 */
const props = defineProps<{ sections: NavSection[] }>();
const open = defineModel<boolean>("open", { required: true });
const { t } = useI18n();
const navigate = useNavigate();
const systemDark = usePreferredDark();

interface Entry {
  id: string;
  label: string;
  hint: string;
  icon: Component;
  run: () => void;
}

const query = ref("");
const active = ref(0);

const entries = computed<Entry[]>(() => {
  const pages = props.sections.flatMap((s) =>
    s.items.map((it) => ({
      id: `page:${s.key}:${it.key}`,
      label: t(`shell.items.${it.label}`),
      hint: t(`shell.sections.${s.key}`),
      icon: it.icon,
      run: () => void navigate({ to: it.to, search: (it.search ?? {}) as never }),
    })),
  );
  const dark = isDarkFor(theme.value, systemDark.value);
  const actions: Entry[] = [
    { id: "action:theme", label: dark ? t("app.themeLight") : t("app.themeDark"), hint: t("shell.palette.action"), icon: dark ? Sun : Moon, run: () => applyTheme(dark ? "light" : "dark") },
    { id: "action:privacy", label: privacyMode.value ? t("privacy.show") : t("privacy.hide"), hint: t("shell.palette.action"), icon: Eye, run: () => setPrivacyMode(!privacyMode.value) },
  ];
  return [...pages, ...actions].filter((e) => matches(query.value, `${e.label} ${e.hint}`));
});

watch(open, (o) => {
  if (o) {
    query.value = "";
    active.value = 0;
  }
});
watch(query, () => (active.value = 0));

function choose(e: Entry | undefined) {
  if (!e) return;
  open.value = false;
  e.run();
}

function onKey(e: KeyboardEvent) {
  const n = entries.value.length;
  if (e.key === "ArrowDown" && n) active.value = (active.value + 1) % n;
  else if (e.key === "ArrowUp" && n) active.value = (active.value - 1 + n) % n;
  else if (e.key === "Enter") choose(entries.value[active.value]);
  else return;
  e.preventDefault();
  document.getElementById(`palette-${active.value}`)?.scrollIntoView({ block: "nearest" });
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/40" />
      <DialogContent
        data-testid="command-palette"
        class="bg-popover text-popover-foreground shadow-pop data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 fixed start-1/2 top-[12vh] z-50 flex max-h-[min(32rem,76vh)] w-[min(36rem,calc(100vw-2rem))] -translate-x-1/2 flex-col overflow-hidden rounded-xl border outline-none rtl:translate-x-1/2"
      >
        <DialogTitle class="sr-only">{{ t("shell.palette.title") }}</DialogTitle>
        <DialogDescription class="sr-only">{{ t("shell.palette.help") }}</DialogDescription>
        <div class="flex items-center gap-3 border-b px-4">
          <Search class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
          <input
            v-model="query"
            data-testid="palette-input"
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            :aria-activedescendant="entries.length ? `palette-${active}` : undefined"
            :aria-label="t('shell.palette.title')"
            :placeholder="t('shell.palette.placeholder')"
            class="placeholder:text-muted-foreground h-12 min-w-0 flex-1 bg-transparent text-sm outline-none"
            autocomplete="off"
            @keydown="onKey"
          />
          <kbd class="text-muted-foreground bg-muted rounded px-1.5 py-0.5 font-sans text-[0.7rem]">Esc</kbd>
        </div>
        <ul id="palette-list" role="listbox" class="min-h-0 flex-1 overflow-y-auto overscroll-contain p-1.5">
          <li
            v-for="(e, i) in entries"
            :id="`palette-${i}`"
            :key="e.id"
            role="option"
            :aria-selected="i === active"
            :data-entry="e.id"
            :class="['flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm', i === active ? 'bg-accent text-accent-foreground' : '']"
            @mousemove="active = i"
            @click="choose(e)"
          >
            <component :is="e.icon" class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate">{{ e.label }}</span>
            <span class="text-muted-foreground shrink-0 text-xs">{{ e.hint }}</span>
            <CornerDownLeft v-if="i === active" class="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
          </li>
          <li v-if="!entries.length" class="text-muted-foreground px-3 py-8 text-center text-sm" data-testid="palette-empty">{{ t("shell.palette.empty") }}</li>
        </ul>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
