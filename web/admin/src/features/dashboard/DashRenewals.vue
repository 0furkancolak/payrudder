<script setup lang="ts">
import { CalendarClock } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import DashSection from "./DashSection.vue";
import type { DashCurrency, Dashboard } from "./lib";

/** The subscriptions that renew next, with the day, the plan and whether the customer is late or still on trial. */
const props = defineProps<{ data: Dashboard; block: DashCurrency; cur: string }>();
const { t } = useI18n();
const fmt = useFormat();

const TONES: Record<string, Tone> = { past_due: "warning", trialing: "info" };
const toneOf = (state: string | undefined): Tone => TONES[state ?? ""] ?? "neutral";
const renewals = computed(() => props.block.renewals ?? []);
</script>

<template>
  <DashSection :title="t('dash.renewals.title')" testid="renewals" :down="data.unavailable?.includes('renewals')" :empty="renewals.length === 0" :empty-text="t('dash.renewals.empty', { currency: cur })">
    <ul class="divide-border/70 divide-y">
      <li v-for="r in renewals" :key="r.subscription_id" class="flex items-center gap-3 py-2.5">
        <span class="bg-brand-soft text-brand-strong flex w-14 shrink-0 flex-col items-center rounded-xl py-1 text-xs font-semibold" aria-hidden="true"><CalendarClock class="mb-0.5 size-3.5" />{{ fmt.date(r.renews_at, "monthDay", "—") }}</span>
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium">{{ r.customer_external_id }}</p>
          <p class="text-muted-foreground truncate text-xs">{{ r.plan_code }}<template v-if="r.interval === 'year'"> · {{ t("billing.packages.perYear") }}</template></p>
        </div>
        <StatusPill :tone="toneOf(r.state)">{{ t(`dash.activity.state.${r.state}`) }}</StatusPill>
      </li>
    </ul>
    <template #footer>
      <p v-if="data.renewals_truncated" class="text-muted-foreground text-xs">{{ t("dash.renewals.truncated") }}</p>
      <p class="text-muted-foreground text-xs">{{ t("dash.renewals.note") }}</p>
    </template>
  </DashSection>
</template>
