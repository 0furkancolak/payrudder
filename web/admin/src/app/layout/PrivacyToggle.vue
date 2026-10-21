<script setup lang="ts">
import { Eye, EyeOff } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { privacyMode, setPrivacyMode } from "@/shared/lib/prefs";

/** The privacy switch of the header: while it is on, amounts are masked everywhere (see formatMinor). Remembered per browser. */
const { t } = useI18n();
</script>

<template>
  <button
    type="button"
    data-testid="privacy-toggle"
    :aria-pressed="privacyMode"
    :aria-label="privacyMode ? t('privacy.show') : t('privacy.hide')"
    :title="`${privacyMode ? t('privacy.show') : t('privacy.hide')} (Alt+P)`"
    :class="[
      'border-border/60 shadow-card inline-flex size-10 shrink-0 items-center justify-center rounded-xl border transition-colors',
      privacyMode ? 'bg-brand-soft text-brand-strong' : 'bg-card text-muted-foreground hover:text-foreground',
    ]"
    @click="setPrivacyMode(!privacyMode)"
  >
    <EyeOff v-if="privacyMode" class="size-4" aria-hidden="true" />
    <Eye v-else class="size-4" aria-hidden="true" />
  </button>
</template>
