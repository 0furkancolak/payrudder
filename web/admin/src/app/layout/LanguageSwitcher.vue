<script setup lang="ts">
import { Globe } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { NativeSelect } from "@/shared/ui/native-select";
import { applyLocale, isLocale, locales, messages, type Locale } from "../i18n";

const { t, locale } = useI18n();

function change(event: Event) {
  const value = (event.target as HTMLSelectElement).value;
  if (isLocale(value)) applyLocale(value);
}
</script>

<template>
  <label class="relative flex items-center">
    <Globe class="text-muted-foreground pointer-events-none absolute start-3 z-10 size-4" />
    <NativeSelect class="shadow-card border-border/60 ps-9" :aria-label="t('app.language')" :model-value="locale" @change="change">
      <option v-for="code in locales" :key="code" :value="code">{{ messages[code as Locale].meta.name }}</option>
    </NativeSelect>
  </label>
</template>
