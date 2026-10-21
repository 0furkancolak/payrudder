<script setup lang="ts">
import { Link, Outlet, useParams } from "@tanstack/vue-router";
import { ChevronLeft } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { StatusPill } from "@/shared/ui/status-pill";
import { useApps } from "./api";

/**
 * The deeper views of one environment of an app (customers, payment links, credits, webhooks): the console's own pages, reused as they are. They speak the
 * tenant API with the person's session token and the tenant as X-Quotify-Tenant, and the gateway lets that through only for a tenant of the person's accounts.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const apps = useApps();

const found = computed(() => {
  for (const app of apps.data.value?.apps ?? []) {
    const tenant = app.tenants?.find((x) => x.id === id.value);
    if (tenant) return { app, env: tenant.environment === "ENVIRONMENT_LIVE" ? "live" : "sandbox" };
  }
  return null;
});

const tab = "text-muted-foreground hover:text-foreground rounded-xl px-4 py-2 text-sm font-medium whitespace-nowrap transition-colors";
const tabActive = "!bg-card !text-foreground shadow-sm";
const tabs = [
  { to: "/app/t/$id/customers", key: "customers" },
  { to: "/app/t/$id/payment-links", key: "paymentLinks" },
  { to: "/app/t/$id/credits", key: "credits" },
  { to: "/app/t/$id/webhooks", key: "webhooks" },
] as const;
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <Link
        v-if="found"
        :to="`/app/apps/${found.app.id}`"
        :search="{ env: found.env } as never"
        class="text-muted-foreground hover:text-foreground mb-1 inline-flex items-center gap-1 text-sm"
      >
        <ChevronLeft class="size-4 rtl:rotate-180" />{{ found.app.name }}
      </Link>
      <Link v-else to="/app" class="text-muted-foreground hover:text-foreground mb-1 inline-flex items-center gap-1 text-sm"><ChevronLeft class="size-4 rtl:rotate-180" />{{ t("product.nav.home") }}</Link>
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ found?.app.name ?? "…" }}</h1>
        <StatusPill v-if="found" :tone="found.env === 'live' ? 'info' : 'neutral'">{{ t(`product.env.${found.env}`) }}</StatusPill>
      </div>
    </header>

    <nav class="bg-muted flex w-fit max-w-full gap-1 overflow-x-auto rounded-2xl p-1" :aria-label="t('product.links.title')">
      <Link v-for="item in tabs" :key="item.key" :to="item.to" :params="{ id }" :class="tab" :active-props="{ class: tabActive, 'aria-current': 'page' }">{{ t(`product.links.${item.key}`) }}</Link>
    </nav>

    <Outlet />
  </section>
</template>
