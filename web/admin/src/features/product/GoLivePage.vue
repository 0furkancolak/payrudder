<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { PageHeader } from "@/shared/ui/page-header";
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
    <PageHeader flush :title="t('shell.items.goLive')" :description="t('product.goLive.pageIntro')">
      <template #badge><StatusPill tone="info">{{ t("product.env.live") }}</StatusPill></template>
    </PageHeader>
    <GoLiveChecklist :app-id="appId" />
    <BillingProfileCard :app-id="appId" />
    <LiveProviderCard :app-id="appId" />
    <PromoteCard :app-id="appId" />
  </section>
</template>
