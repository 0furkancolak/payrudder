<script setup lang="ts">
import { RouterProvider } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { ConfirmDialog } from "@/shared/ui/confirm-dialog";
import { Toaster } from "@/shared/ui/sonner";
import { router } from "./router";

const { t } = useI18n();
/** Served by /env.js (PAYRUDDER_DEMO): the stack is a demo, no real provider is ever called. */
const demo = window.__PAYRUDDER__?.demo === true;
</script>

<template>
  <div v-if="demo" class="bg-warning-soft text-warning border-b px-4 py-1.5 text-center text-sm font-medium" role="status">{{ t("app.demo") }}</div>
  <RouterProvider :router="router" />
  <ConfirmDialog />
  <Toaster position="bottom-right" />
</template>
