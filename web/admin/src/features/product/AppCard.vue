<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { formatMinor } from "@/shared/lib/money";
import { Sparkline } from "@/shared/ui/sparkline";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useOverview, type App, type Environment } from "./api";
import { countOf, revenueByCurrency, seriesOf, tenantOf } from "./lib";

/** One app on the organisation's home: its status, the last 30 days of money that arrived, a trend line, and how many payments need a look. */
const props = defineProps<{ app: App }>();
const { t, locale } = useI18n();

// The live environment is the one that matters; a sandbox-only deployment shows the sandbox.
const env = computed<Environment>(() => (tenantOf(props.app, "live") ? "live" : "sandbox"));
const overview = useOverview(
  () => props.app.id ?? "",
  env,
);
const revenue = computed(() => revenueByCurrency({ currency: overview.data.value?.currency ?? "", payments: overview.data.value?.payments }));
const lead = computed(() => revenue.value[0]);
const failed = computed(() => countOf(overview.data.value?.payments, "failed"));
const status = computed(() => overview.data.value?.status);
</script>

<template>
  <Link
    :to="`/app/apps/${app.id}`"
    class="surface group flex flex-col gap-5 p-6 transition-shadow hover:shadow-pop focus-visible:outline-2 focus-visible:outline-offset-2"
    :aria-label="app.name"
  >
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <h3 class="truncate text-lg font-semibold tracking-tight">{{ app.name }}</h3>
        <p class="text-muted-foreground mono truncate text-xs">{{ app.slug }}</p>
      </div>
      <Skeleton v-if="overview.isPending.value" class="h-6 w-20 rounded-full" />
      <StatusPill v-else-if="status" :tone="toneOf(status)">{{ t(`product.appStatus.${status}`) }}</StatusPill>
      <StatusPill v-else tone="neutral">{{ t("product.appStatus.unknown") }}</StatusPill>
    </div>

    <div class="flex items-end justify-between gap-4">
      <div class="min-w-0">
        <p class="text-muted-foreground text-xs">{{ t("product.card.revenue", { days: overview.data.value?.window_days ?? 30 }) }}</p>
        <Skeleton v-if="overview.isPending.value" class="mt-1 h-8 w-28" />
        <p v-else-if="lead" class="truncate text-3xl font-semibold tracking-tight tabular">{{ formatMinor(lead.minor, lead.currency, locale) }}</p>
        <p v-else-if="overview.data.value?.payments" class="text-3xl font-semibold tracking-tight tabular">{{ formatMinor(0, overview.data.value?.currency, locale) }}</p>
        <p v-else class="text-muted-foreground text-sm">{{ t("product.card.unavailable") }}</p>
        <p v-if="revenue.length > 1" class="text-muted-foreground mt-0.5 truncate text-xs tabular">
          {{ revenue.slice(1).map((r) => formatMinor(r.minor, r.currency, locale)).join(" · ") }}
        </p>
      </div>
      <Sparkline v-if="lead" :values="seriesOf(overview.data.value?.payments, lead.currency)" :width="112" :height="44" />
    </div>

    <div class="text-muted-foreground flex items-center justify-between border-t pt-4 text-xs">
      <span v-if="failed > 0" class="text-destructive font-medium">{{ t("product.card.failed", { n: failed }) }}</span>
      <span v-else>{{ t("product.card.environments") }}</span>
      <span class="text-primary font-medium group-hover:underline">{{ t("product.card.open") }}</span>
    </div>
  </Link>
</template>
