<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { Activity, CreditCard, Webhook } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import type { AppOverview } from "./api";

/**
 * Whether the app's plumbing is healthy: the payments service answers, there is somewhere for webhooks to go and nothing has been failing, and which
 * payment providers are connected. Platform-wide queues are not here: they belong to the operator.
 */
const props = defineProps<{ overview: AppOverview; tenantId: string }>();
const { t } = useI18n();

const h = computed(() => props.overview.health);
const down = (section: string) => props.overview.unavailable?.includes(section as never) ?? false;
const providers = computed(() => (h.value?.providers ?? []).map((p) => p.provider).join(", "));
</script>

<template>
  <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.health.title')">
    <div class="flex items-center justify-between gap-3">
      <h2 class="text-lg font-semibold tracking-tight">{{ t("product.health.title") }}</h2>
      <StatusPill :tone="toneOf(overview.status)">{{ t(`product.appStatus.${overview.status}`) }}</StatusPill>
    </div>
    <ul class="flex flex-col gap-3 text-sm">
      <li class="flex items-center gap-3">
        <span class="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl" aria-hidden="true"><Activity class="size-4" /></span>
        <span class="flex-1">{{ t("product.health.payments") }}</span>
        <StatusPill :tone="h?.payments_reachable ? 'success' : 'danger'">{{ h?.payments_reachable ? t("product.health.up") : t("product.health.down") }}</StatusPill>
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
        <span v-if="down('webhooks')" class="text-muted-foreground">{{ t("product.unavailable") }}</span>
        <StatusPill v-else-if="(h?.webhook_failed ?? 0) > 0" tone="danger">{{ t("product.health.failedDeliveries", { n: `${h?.webhook_failed}${h?.webhook_failed_more ? "+" : ""}` }) }}</StatusPill>
        <Link v-else-if="(h?.webhook_endpoints ?? 0) === 0" :to="`/app/t/${tenantId}/webhooks`" class="text-primary text-sm font-medium hover:underline">{{ t("product.health.addEndpoint") }}</Link>
        <StatusPill v-else tone="success">{{ t("product.health.endpoints", { n: h?.webhook_endpoints ?? 0 }, h?.webhook_endpoints ?? 0) }}</StatusPill>
      </li>
    </ul>
  </section>
</template>
