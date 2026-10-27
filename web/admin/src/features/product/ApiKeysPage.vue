<script setup lang="ts">
import { useParams, useSearch } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { defaultEnv, isEnvironment, type Environment } from "@/shared/lib/environment";
import { PageHeader } from "@/shared/ui/page-header";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useAccount, useApp } from "./api";
import KeysPanel from "./KeysPanel.vue";

/**
 * The API keys of one environment of an app. The environment is the address's (`?env=`); without one it is where the person last was in this app, decided once
 * the app has arrived so the page never shows one environment and then flips to the other. Sandbox keys are made here in the sandbox view and live keys in the
 * live view, never both from one place.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const search = useSearch({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);
const account = useAccount();
const canManage = computed(() => (app.data.value?.role ?? account.data.value?.role) !== "viewer");
const env = computed<Environment | null>(() => {
  const v = (search.value as { env?: string }).env;
  if (isEnvironment(v)) return v;
  return app.data.value?.app ? defaultEnv(app.data.value.app) : null;
});
</script>

<template>
  <section class="flex flex-col gap-6">
    <PageHeader flush :title="t('shell.items.apiKeys')" :description="t('product.apiKeysPage.intro')">
      <template #badge><StatusPill v-if="env" :tone="env === 'live' ? 'info' : 'neutral'" data-testid="keys-env">{{ t(`product.env.${env}`) }}</StatusPill></template>
    </PageHeader>
    <Skeleton v-if="!env" class="h-48 rounded-3xl" aria-busy="true" />
    <KeysPanel v-else :app-id="appId" :can-manage="canManage" :env="env" />
  </section>
</template>
