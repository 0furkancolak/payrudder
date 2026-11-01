<script setup lang="ts">
import { Check, Circle, TriangleAlert } from "@lucide/vue";
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useContractSettings, useContractVersions } from "./api";
import { agreementsAdvisory } from "./lib";

/**
 * The go-live checklist's advice about the agreements a seller shows its customers. It is advice, not a step: it is not counted and blocks nothing (the
 * server's list is the five steps). Nothing is asked of a seller who requires no agreement ("needed if you sell to consumers"); a required one is ready
 * once every required kind is published, and a warning while a required kind's current text still carries the starter notice. `tenantId` is the live tenant.
 */
const props = defineProps<{ tenantId: string; compact?: boolean }>();
const { t } = useI18n();
const settings = useContractSettings(() => props.tenantId);
const versions = useContractVersions(() => props.tenantId);
// The advice needs both answers; a seller whose agreements cannot be read gets no row rather than a wrong one.
const state = computed(() => (settings.isSuccess.value && versions.isSuccess.value ? agreementsAdvisory(settings.data.value, versions.data.value) : null));
</script>

<template>
  <div v-if="state" class="flex items-start gap-3 rounded-2xl px-3 py-2" data-testid="go-live-agreements" :data-state="state">
    <span class="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full" :class="state === 'ready' ? 'bg-success-soft text-success' : state === 'optional' ? 'text-muted-foreground' : 'bg-warning-soft text-warning'" aria-hidden="true">
      <Check v-if="state === 'ready'" class="size-3.5" /><TriangleAlert v-else-if="state !== 'optional'" class="size-3.5" /><Circle v-else class="size-4" />
    </span>
    <div class="min-w-0 flex-1">
      <p class="text-sm font-medium">{{ t("product.goLive.agreements.title") }}<span class="text-muted-foreground ms-2 text-xs font-normal">{{ t("product.goLive.agreements.advisory") }}</span></p>
      <p v-if="!compact || state !== 'optional'" class="text-muted-foreground text-xs">{{ t(`product.goLive.agreements.${state}`) }}</p>
    </div>
    <Link v-if="!compact" :to="`/app/t/${tenantId}/contracts`" class="text-primary shrink-0 text-xs font-medium hover:underline" data-testid="go-live-go-agreements">{{ t("product.goLive.open") }}</Link>
  </div>
</template>
