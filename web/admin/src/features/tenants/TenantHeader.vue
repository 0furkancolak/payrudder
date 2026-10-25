<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { envOf } from "@/shared/lib/environment";
import { currencyInfo } from "@/shared/lib/intl";
import { StatusPill } from "@/shared/ui/status-pill";
import type { Tenant } from "@/shared/api/client";

/**
 * The head of every tenant page, for the person and for the operator: the name of the section (the sidebar already says where you are), and under it
 * the app, the environment and the currency and time zone the tenant bills in. The navigation itself is the sidebar.
 */
const props = defineProps<{ title: string; name?: string; tenant?: Tenant | undefined }>();
const { t, locale } = useI18n();
const env = computed(() => (props.tenant ? envOf(props.tenant.environment) : ""));
const currency = computed(() => (props.tenant?.currency ? currencyInfo(props.tenant.currency, locale.value) : null));
</script>

<template>
  <header class="mb-6 flex flex-wrap items-end justify-between gap-3 px-1">
    <div class="min-w-0">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl" data-testid="page-title">{{ title }}</h1>
      <p class="text-muted-foreground mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
        <span v-if="name" class="font-medium">{{ name }}</span>
        <StatusPill v-if="env" :tone="env === 'live' ? 'info' : 'neutral'">{{ t(`product.env.${env}`) }}</StatusPill>
        <span v-if="currency && tenant" :title="currency.name">{{ currency.code }} · {{ tenant.timezone }}</span>
      </p>
    </div>
    <div class="flex items-center gap-2"><slot /></div>
  </header>
</template>
