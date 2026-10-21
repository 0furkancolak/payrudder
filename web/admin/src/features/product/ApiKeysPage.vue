<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useAccount, useApp } from "./api";
import KeysPanel from "./KeysPanel.vue";

/** The API keys of an app on a page of their own (they used to sit at the foot of the dashboard). */
const { t } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);
const account = useAccount();
const canManage = computed(() => (app.data.value?.role ?? account.data.value?.role) !== "viewer");
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.apiKeys") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("product.apiKeysPage.intro") }}</p>
    </header>
    <KeysPanel :app-id="appId" :can-manage="canManage" />
  </section>
</template>
