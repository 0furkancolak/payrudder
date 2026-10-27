<script setup lang="ts">
import { FlaskConical } from "@lucide/vue";
import { useI18n } from "vue-i18n";

/**
 * Says which environment the page is about, for as long as it is the sandbox: a thin accent stripe across the very top of the window (fixed, so it stays when
 * the page scrolls) and a line under the header. Live has neither: it is the unmarked, real one. The text is why the colour is there, so the colour is
 * never the only signal.
 */
defineProps<{ show: boolean }>();
const { t } = useI18n();
</script>

<template>
  <template v-if="show">
    <div class="bg-warning pointer-events-none fixed inset-x-0 top-0 z-[70] h-1" aria-hidden="true" data-testid="sandbox-stripe"></div>
    <p class="bg-warning-soft text-warning mx-4 mt-1 flex max-w-[1320px] items-center gap-2 rounded-xl px-3 py-1 text-xs font-medium sm:rounded-2xl sm:px-4 sm:py-2 sm:text-sm md:mx-8 xl:mx-auto" role="status" data-testid="sandbox-banner">
      <FlaskConical class="size-4 shrink-0" aria-hidden="true" />{{ t("product.env.banner") }}
    </p>
  </template>
</template>
