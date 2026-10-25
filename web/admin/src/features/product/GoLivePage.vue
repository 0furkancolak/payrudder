<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { StatusPill } from "@/shared/ui/status-pill";
import BillingProfileCard from "./BillingProfileCard.vue";
import GoLiveChecklist from "./GoLiveChecklist.vue";
import LiveProviderCard from "./LiveProviderCard.vue";
import PromoteCard from "./PromoteCard.vue";

/** Going live: the checklist and its button, the billing profile and the payment provider the owner fills in, and the copy of the sandbox's configuration to live. */
const { t } = useI18n();
const params = useParams({ strict: false });
const appId = computed(() => String(params.value.appId ?? ""));
</script>

<template>
  <section class="flex flex-col gap-6">
    <header class="px-1">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-3xl font-semibold tracking-tight md:text-4xl">{{ t("shell.items.goLive") }}</h1>
        <StatusPill tone="info">{{ t("product.env.live") }}</StatusPill>
      </div>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">{{ t("product.goLive.pageIntro") }}</p>
    </header>
    <GoLiveChecklist :app-id="appId" />
    <BillingProfileCard :app-id="appId" />
    <LiveProviderCard :app-id="appId" />
    <PromoteCard :app-id="appId" />
  </section>
</template>
