<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Activity, CreditCard, Webhook } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import type { Dashboard } from "./lib";

/** Whether the app can take money: the payments service answers, a provider is connected, and the webhook endpoints work. `base` is where the app's pages live. */
const props = defineProps<{ data: Dashboard; base: string }>();
const { t } = useI18n();

const health = computed(() => props.data.health);
const webhooksDown = computed(() => props.data.unavailable?.includes("webhooks") ?? false);
const providers = computed(() => (health.value?.providers ?? []).map((p) => p.provider).join(", "));
const failedCount = computed(() => `${health.value?.webhook_failed}${health.value?.webhook_failed_more ? "+" : ""}`);
const endpoints = computed(() => health.value?.webhook_endpoints ?? 0);
</script>

<template>
  <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.health.title')" data-testid="health">
    <div class="flex items-center justify-between gap-3">
      <h2 class="text-lg font-semibold tracking-tight">{{ t("product.health.title") }}</h2>
      <StatusPill :tone="toneOf(data.status)">{{ t(`product.appStatus.${data.status}`) }}</StatusPill>
    </div>
    <ul class="grid gap-4 text-sm md:grid-cols-2 xl:grid-cols-3">
      <li class="flex items-center gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Activity class="size-4" /></span>
        <span class="flex-1">{{ t("product.health.payments") }}</span>
        <StatusPill :tone="health?.payments_reachable ? 'success' : 'danger'">{{ health?.payments_reachable ? t("product.health.up") : t("product.health.down") }}</StatusPill>
      </li>
      <li class="flex items-center gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><CreditCard class="size-4" /></span>
        <span class="flex-1">{{ t("product.health.providers") }}</span>
        <span v-if="providers" class="text-muted-foreground capitalize">{{ providers }}</span>
        <span v-else class="text-muted-foreground">{{ t("product.health.noProviders") }}</span>
      </li>
      <li class="flex items-center gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Webhook class="size-4" /></span>
        <span class="flex-1">{{ t("product.health.webhooks") }}</span>
        <span v-if="webhooksDown" class="text-muted-foreground">{{ t("product.unavailable") }}</span>
        <StatusPill v-else-if="(health?.webhook_failed ?? 0) > 0" tone="danger">{{ t("product.health.failedDeliveries", { n: failedCount }) }}</StatusPill>
        <Link v-else-if="endpoints === 0" :to="`${base}/webhooks`" class="text-primary text-sm font-medium hover:underline">{{ t("product.health.addEndpoint") }}</Link>
        <StatusPill v-else tone="success">{{ t("product.health.endpoints", { n: endpoints }, endpoints) }}</StatusPill>
      </li>
    </ul>
  </section>
</template>
