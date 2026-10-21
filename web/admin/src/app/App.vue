<script setup lang="ts">
import { RouterProvider } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { Toaster } from "@/shared/ui/sonner";
import { router } from "./router";

const { t } = useI18n();
/** Served by /env.js (QUOTIFY_DEMO): the stack is a demo, no real provider is ever called. */
const demo = window.__QUOTIFY__?.demo === true;
</script>

<template>
  <div v-if="demo" class="bg-warning/15 text-foreground border-b px-4 py-1.5 text-center text-sm" role="status">{{ t("app.demo") }}</div>
  <RouterProvider :router="router" />
  <Toaster position="bottom-right" />
</template>
