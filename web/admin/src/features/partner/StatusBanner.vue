<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { isSandbox, type Affiliation } from "./logic";

/** What the partner needs to know about the state of an affiliation, in words. Pending and suspended partners still see their data. */
defineProps<{ affiliation: Affiliation }>();
const { t } = useI18n();
</script>

<template>
  <div class="flex flex-col gap-2">
    <p v-if="affiliation.status === 'pending'" data-testid="banner-pending" role="status" class="m-0 bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm font-medium">{{ t("partner.banner.pending") }}</p>
    <p v-else-if="affiliation.status === 'suspended'" data-testid="banner-suspended" role="status" class="m-0 bg-destructive-soft text-destructive rounded-2xl px-4 py-3 text-sm font-medium">{{ t("partner.banner.suspended") }}</p>
    <p v-if="isSandbox(affiliation)" data-testid="banner-sandbox" role="status" class="bg-warning-soft text-warning m-0 rounded-2xl px-4 py-3 text-sm">{{ t("partner.banner.sandbox") }}</p>
  </div>
</template>
