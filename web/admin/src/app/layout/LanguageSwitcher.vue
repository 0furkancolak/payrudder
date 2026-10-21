<script setup lang="ts">
import { Check, Globe } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/shared/ui/dropdown-menu";
import { applyLocale, locales, messages, type Locale } from "../i18n";

/** The language menu: a globe button that opens the list of languages, each in its own name. Remembered per browser. */
const { t, locale } = useI18n();
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <button
        type="button"
        data-testid="language-switcher"
        class="bg-card text-muted-foreground hover:text-foreground border-border/60 shadow-card inline-flex h-10 shrink-0 items-center gap-1.5 rounded-xl border px-2.5 text-xs font-semibold uppercase"
        :aria-label="t('app.language')"
        :title="t('app.language')"
      >
        <Globe class="size-4" aria-hidden="true" />{{ locale }}
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="min-w-44">
      <DropdownMenuItem v-for="code in locales" :key="code" class="rounded-xl" :data-testid="`lang-${code}`" @select="applyLocale(code)">
        <span class="flex-1">{{ messages[code as Locale].meta.name }}</span>
        <Check v-if="code === locale" class="size-4" aria-hidden="true" />
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
