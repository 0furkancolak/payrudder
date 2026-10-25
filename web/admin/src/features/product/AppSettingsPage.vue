<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { envOf } from "@/shared/lib/environment";
import { currencyInfo, formatOffset, zoneMinutes } from "@/shared/lib/intl";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApp } from "./api";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";

/**
 * What an app was created with: its name and address, and for each environment its currency, time zone, grace period and plan. The currency and the
 * time zone are fixed when the app is created (they decide how every invoice is worked out), so this page shows them and does not offer to change them.
 * Packages and prices in other currencies are added per package, not here.
 */
const { t, locale } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
const app = useApp(appId);

const envs = computed(() =>
  (app.data.value?.app?.tenants ?? []).map((x) => ({
    id: x.id ?? "",
    env: envOf(x.environment),
    currency: x.currency ?? "",
    info: currencyInfo(x.currency ?? FALLBACK_CURRENCY, locale.value),
    timezone: x.timezone ?? "",
    offset: formatOffset(zoneMinutes(x.timezone ?? "UTC")),
    grace: x.grace_period ?? "",
    tier: (x.tier ?? "").replace("TENANT_TIER_", "").toLowerCase(),
  })),
);
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.appSettings") }}</h1>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("product.appSettings.intro") }}</p>
    </header>

    <Skeleton v-if="app.isPending.value" class="h-40 rounded-3xl" />
    <template v-else>
      <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.appSettings.general')">
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.appSettings.general") }}</h2>
        <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt class="text-muted-foreground">{{ t("product.createApp.name") }}</dt>
          <dd class="font-medium">{{ app.data.value?.app?.name }}</dd>
          <dt class="text-muted-foreground">{{ t("product.appSettings.slug") }}</dt>
          <dd class="mono">{{ app.data.value?.app?.slug }}</dd>
          <dt class="text-muted-foreground">{{ t("product.appSettings.id") }}</dt>
          <dd class="mono break-all">{{ app.data.value?.app?.id }}</dd>
        </dl>
      </section>

      <div class="grid gap-5 lg:grid-cols-2">
        <section v-for="e in envs" :key="e.id" class="surface flex flex-col gap-4 p-6" :aria-label="t(`product.env.${e.env}`)">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-semibold tracking-tight">{{ t(`product.env.${e.env}`) }}</h2>
            <StatusPill :tone="e.env === 'live' ? 'info' : 'neutral'">{{ e.tier }}</StatusPill>
          </div>
          <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
            <dt class="text-muted-foreground">{{ t("product.appSettings.currency") }}</dt>
            <dd class="font-medium" :data-testid="`currency-${e.env}`">{{ e.currency }} · {{ e.info.name }} ({{ e.info.symbol }})</dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.timezone") }}</dt>
            <dd class="font-medium" :data-testid="`timezone-${e.env}`">{{ e.timezone }} <span class="text-muted-foreground tabular-nums">{{ e.offset }}</span></dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.grace") }}</dt>
            <dd class="font-medium">{{ e.grace }}</dd>
            <dt class="text-muted-foreground">{{ t("product.appSettings.tenant") }}</dt>
            <dd class="mono text-xs break-all">{{ e.id }}</dd>
          </dl>
        </section>
      </div>

      <p class="text-muted-foreground max-w-2xl text-sm">
        {{ t("product.appSettings.moreCurrencies") }}
        <Link v-if="envs[0]" :to="`/app/t/${envs[0].id}/packages`" class="text-primary font-medium underline-offset-4 hover:underline">{{ t("shell.items.packages") }}</Link>
      </p>
    </template>
  </section>
</template>
