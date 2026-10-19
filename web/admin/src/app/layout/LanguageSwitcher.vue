<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { applyLocale, isLocale, locales, messages, type Locale } from "../i18n";

const { t, locale } = useI18n();

function change(event: Event) {
  const value = (event.target as HTMLSelectElement).value;
  if (isLocale(value)) applyLocale(value);
}
</script>

<template>
  <label class="muted">
    <span class="sr-only" style="position: absolute; left: -9999px">{{ t("app.language") }}</span>
    <select :aria-label="t('app.language')" :value="locale" @change="change">
      <option v-for="code in locales" :key="code" :value="code">{{ messages[code as Locale].meta.name }}</option>
    </select>
  </label>
</template>
