<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { Subscription } from "@/shared/api/client";
import { setKind } from "@/shared/lib/sheets";
import { Button } from "@/shared/ui/button";
import { useSubscriptions } from "./api";
import { successorOf } from "./lib";

/**
 * A change of plan ends the subscription and starts another (`changed_from_id`). The sheet of either points to the other, so a person who changed a plan
 * is not left on the old, cancelled one wondering where the new one went.
 */
const props = defineProps<{ tenantId: string; subscription: Subscription }>();
const { t } = useI18n();
const subs = useSubscriptions(() => props.tenantId);
const list = computed(() => subs.data.value?.subscriptions ?? []);
const next = computed(() => successorOf(list.value, props.subscription.id));
const previous = computed(() => (props.subscription.changed_from_id ? list.value.find((s) => s.id === props.subscription.changed_from_id) : undefined));
const label = (s: Subscription) => `${s.plan_code} v${s.plan_version}`;
</script>

<template>
  <div v-if="next || previous" class="bg-muted flex flex-col gap-2 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="change-trail">
    <div v-if="next" class="flex flex-wrap items-center justify-between gap-2">
      <span>{{ t("billing.subscriptions.trail.replacedBy", { plan: label(next) }) }}</span>
      <Button size="sm" variant="outline" data-testid="trail-next" @click="setKind('subscription', next.id)">{{ t("billing.subscriptions.trail.open") }}</Button>
    </div>
    <div v-if="previous" class="flex flex-wrap items-center justify-between gap-2">
      <span>{{ t("billing.subscriptions.trail.changedFrom", { plan: label(previous) }) }}</span>
      <Button size="sm" variant="outline" data-testid="trail-previous" @click="setKind('subscription', previous.id)">{{ t("billing.subscriptions.trail.open") }}</Button>
    </div>
  </div>
</template>
