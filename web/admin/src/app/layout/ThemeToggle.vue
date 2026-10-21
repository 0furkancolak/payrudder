<script setup lang="ts">
import { Moon, Sun } from "@lucide/vue";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { applyTheme, isDark } from "@/shared/lib/theme";

const { t } = useI18n();
const dark = ref(isDark());

function toggle() {
  applyTheme(dark.value ? "light" : "dark");
  dark.value = !dark.value;
}
</script>

<template>
  <button
    type="button"
    class="bg-card text-muted-foreground hover:text-foreground border-border/60 shadow-card inline-flex size-10 items-center justify-center rounded-xl border transition-colors"
    :aria-label="dark ? t('app.themeLight') : t('app.themeDark')"
    :title="dark ? t('app.themeLight') : t('app.themeDark')"
    @click="toggle"
  >
    <Sun v-if="dark" class="size-4" />
    <Moon v-else class="size-4" />
  </button>
</template>
