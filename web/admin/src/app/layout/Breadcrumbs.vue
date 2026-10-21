<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ChevronRight } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import type { Crumb } from "./useShell";

/** Where the page sits: organisation > app > section. The last crumb is the current page; the others are links. On a phone only the current page shows. */
defineProps<{ crumbs: Crumb[] }>();
const { t } = useI18n();
</script>

<template>
  <nav :aria-label="t('shell.breadcrumbs')" class="min-w-0">
    <ol class="flex min-w-0 items-center gap-1.5 text-sm">
      <li v-for="(c, i) in crumbs" :key="i" :class="['flex min-w-0 items-center gap-1.5', i < crumbs.length - 1 && 'max-sm:hidden']">
        <ChevronRight v-if="i > 0" class="text-muted-foreground size-3.5 shrink-0 rtl:rotate-180 max-sm:hidden" aria-hidden="true" />
        <Link v-if="c.to && i < crumbs.length - 1" :to="c.to" :search="(c.search ?? {}) as never" class="text-muted-foreground hover:text-foreground truncate">{{ c.label }}</Link>
        <span v-else class="text-foreground truncate font-medium" :aria-current="i === crumbs.length - 1 ? 'page' : undefined">{{ c.label }}</span>
      </li>
    </ol>
  </nav>
</template>
