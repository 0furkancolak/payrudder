<script setup lang="ts">
import { RouterProvider } from "@tanstack/vue-router";
import { ConfigProvider } from "reka-ui";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Toaster } from "@/shared/ui/sonner";
import { languages, type Locale } from "./i18n";
import { router } from "./router";

const { t, locale } = useI18n();
/** The layers (popovers, menus, selects, tooltips) are drawn outside the page and take their reading direction from here, not from the page: without it they stay left to right in Arabic. */
const dir = computed(() => languages[locale.value as Locale]?.dir ?? "ltr");
/** Served by /env.js (PAYRUDDER_DEMO): the stack is a demo, no real provider is ever called. */
const demo = window.__PAYRUDDER__?.demo === true;
</script>

<template>
  <div v-if="demo" class="bg-warning-soft text-warning border-b px-4 py-1.5 text-center text-sm font-medium" role="status">{{ t("app.demo") }}</div>
  <ConfigProvider :dir="dir">
    <RouterProvider :router="router" />
    <ConfirmDialog />
    <Toaster position="bottom-right" />
  </ConfigProvider>
</template>
