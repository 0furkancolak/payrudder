<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useSubscriptions } from "@/features/subscriptions/api";
import { useFormat } from "@/shared/lib/format";
import { enumWord, label } from "@/shared/lib/labels";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, type Tone } from "@/shared/ui/status-pill";
import RelatedRow from "./RelatedRow.vue";
import RelatedSection from "./RelatedSection.vue";

/** The subscriptions of one customer (the first `limit` when given); each opens the subscription's sheet. */
const props = defineProps<{ tenantId: string; customer: string; limit?: number; bare?: boolean; emptyText?: string }>();
const { t, te } = useI18n();
const fmt = useFormat();
const subs = useSubscriptions(() => props.tenantId);
const all = computed(() => (subs.data.value?.subscriptions ?? []).filter((s) => s.customer_external_id === props.customer));
const rows = computed(() => (props.limit ? all.value.slice(0, props.limit) : all.value));
const TONES: Record<string, Tone> = { active: "success", trialing: "info", past_due: "danger", paused: "warning", cancelled: "neutral" };
const word = (state?: string) => enumWord(state, "SUBSCRIPTION_STATE_");
// The state as a status word, the way the customer list writes it ("Past due"); a state that list does not know keeps its enum label.
const STATUS_KEYS: Record<string, string> = { active: "active", trialing: "trialing", past_due: "pastDue", paused: "paused", cancelled: "cancelled" };
const stateText = (state?: string) => {
  const key = STATUS_KEYS[word(state)];
  return key ? t(`billing.customers.subscriptionStatus.${key}`) : label(t, te, "subscriptionState", word(state));
};
</script>

<template>
  <RelatedSection :title="t('related.subscriptions')" :empty="!subs.isPending.value && rows.length === 0" :hide-title="bare" :empty-text="emptyText">
    <template v-if="$slots.action && all.length > rows.length" #action><slot name="action" /></template>
    <template v-if="subs.isPending.value">
      <li><Skeleton class="h-12 w-full rounded-xl" /></li>
    </template>
    <RelatedRow v-for="s in rows" v-else :id="s.id ?? ''" :key="s.id" :tenant-id="tenantId" kind="subscription">
      <span class="flex min-w-0 flex-col">
        <span class="mono truncate font-medium">{{ s.plan_code }} v{{ s.plan_version }}</span>
        <span class="text-muted-foreground text-xs">{{ [s.currency, (s.quantity ?? 1) > 1 ? t("billing.subscriptions.seatsCount", { n: s.quantity }, s.quantity ?? 1) : "", fmt.date(s.start_time, "day")].filter(Boolean).join(" · ") }}</span>
      </span>
      <StatusPill :tone="TONES[word(s.state)] ?? 'neutral'">{{ stateText(s.state) }}</StatusPill>
    </RelatedRow>
  </RelatedSection>
</template>
