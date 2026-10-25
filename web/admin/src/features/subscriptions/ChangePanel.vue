<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { type Subscription } from "@/shared/api/client";
import { useErrorText } from "@/shared/lib/errors";
import { Alert } from "@/shared/ui/alert";
import { Button } from "@/shared/ui/button";
import { NativeSelect } from "@/shared/ui/native-select";
import type { Package } from "@/features/packages";
import { useSubscriptionActions, type ChangeInput } from "./api";

const props = defineProps<{ subscription: Subscription; packages: Package[]; tenantId: string; environment: string }>();
const { t } = useI18n();
const fmt = useFormat();
const errorText = useErrorText();
const act = useSubscriptionActions(
  () => props.tenantId,
  () => props.environment,
);

const plan = ref("");
const interval = ref<ChangeInput["interval"]>("");
const timing = ref<ChangeInput["timing"]>("");
const proration = ref<ChangeInput["proration"]>("");
const input = computed<ChangeInput>(() => ({ id: props.subscription.id ?? "", plan_code: plan.value, interval: interval.value, timing: timing.value, proration: proration.value }));
const nothingChosen = computed(() => plan.value === "" && interval.value === "");
const quote = computed(() => act.previewChange.data.value);
const scheduled = computed(() => props.subscription.scheduled_change);
const date = (v?: string | null) => fmt.date(v, "day", "—");
const money = (v?: string) => fmt.money(v, props.subscription.currency);
const intervalName = (v?: string) => (v === "SUBSCRIPTION_INTERVAL_YEAR" ? t("billing.subscriptions.yearly") : t("billing.subscriptions.monthly"));
/** What the change does with money now, in words: invoiced, credited or nothing. */
const outcome = computed(() => {
  const net = quote.value?.net_minor;
  if (net === undefined || net === "0") return t("billing.subscriptions.change.nothing");
  return net.startsWith("-") ? t("billing.subscriptions.change.creditAdded", { amount: money(net.slice(1)) }) : t("billing.subscriptions.change.due", { amount: money(net) });
});
const failure = computed(() => {
  for (const m of [act.previewChange, act.change, act.cancelScheduledChange]) if (m.error.value) return errorText(m.error.value);
  return "";
});

function reset() {
  act.previewChange.reset();
}
function apply() {
  act.change.mutate(input.value, {
    onSuccess: () => {
      plan.value = interval.value = timing.value = proration.value = "";
      reset();
    },
  });
}
</script>

<template>
  <section class="grid gap-3" role="region" :aria-label="t('billing.subscriptions.change.title')">
    <h3 class="text-base font-semibold">{{ t("billing.subscriptions.change.title") }}</h3>
    <Alert v-if="scheduled" role="status">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span>
          {{ t("billing.subscriptions.change.scheduled", { plan: scheduled.plan_code ?? "", interval: intervalName(scheduled.interval), date: date(scheduled.effective_time) }) }}
        </span>
        <Button size="sm" variant="outline" :disabled="act.cancelScheduledChange.isPending.value" @click="act.cancelScheduledChange.mutate(subscription.id ?? '')">
          {{ t("billing.subscriptions.change.withdraw") }}
        </Button>
      </div>
    </Alert>
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <NativeSelect v-model="plan" :aria-label="t('billing.subscriptions.change.plan')" @change="reset">
        <option value="">{{ t("billing.subscriptions.change.keepPlan") }}</option>
        <option v-for="p in packages" :key="p.plan.id" :value="p.plan.code">{{ p.plan.code }}</option>
      </NativeSelect>
      <NativeSelect v-model="interval" :aria-label="t('billing.subscriptions.change.interval')" @change="reset">
        <option value="">{{ t("billing.subscriptions.change.keepInterval") }}</option>
        <option value="SUBSCRIPTION_INTERVAL_MONTH">{{ t("billing.subscriptions.monthly") }}</option>
        <option value="SUBSCRIPTION_INTERVAL_YEAR">{{ t("billing.subscriptions.yearly") }}</option>
      </NativeSelect>
      <NativeSelect v-model="timing" :aria-label="t('billing.subscriptions.change.when')" @change="reset">
        <option value="">{{ t("billing.subscriptions.change.auto") }}</option>
        <option value="CHANGE_TIMING_IMMEDIATELY">{{ t("billing.subscriptions.change.now") }}</option>
        <option value="CHANGE_TIMING_PERIOD_END">{{ t("billing.subscriptions.change.atEnd") }}</option>
      </NativeSelect>
      <NativeSelect v-model="proration" :aria-label="t('billing.subscriptions.change.proration')" @change="reset">
        <option value="">{{ t("billing.subscriptions.change.prorate") }}</option>
        <option value="CHANGE_PRORATION_FULL">{{ t("billing.subscriptions.change.full") }}</option>
        <option value="CHANGE_PRORATION_NONE">{{ t("billing.subscriptions.change.none") }}</option>
      </NativeSelect>
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" :disabled="nothingChosen || act.previewChange.isPending.value" @click="act.previewChange.mutate(input)">{{ t("billing.subscriptions.change.preview") }}</Button>
      <Button :disabled="nothingChosen || quote === undefined || act.change.isPending.value" @click="apply">{{ t("billing.subscriptions.change.apply") }}</Button>
    </div>
    <dl v-if="quote" class="bg-secondary/50 grid gap-1 rounded-2xl px-4 py-3 text-sm sm:grid-cols-2" :aria-label="t('billing.subscriptions.change.quote')">
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.effective") }}</dt>
      <dd>{{ quote.timing === "CHANGE_TIMING_PERIOD_END" ? t("billing.subscriptions.change.atEnd") : t("billing.subscriptions.change.now") }} · {{ date(quote.effective_time) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.credit") }}</dt>
      <dd class="tabular-nums">{{ money(quote.credit_minor) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.charge") }}</dt>
      <dd class="tabular-nums">{{ money(quote.charge_minor) }}</dd>
      <dt class="font-medium">{{ t("billing.subscriptions.change.result") }}</dt>
      <dd class="font-medium tabular-nums">{{ outcome }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.next") }}</dt>
      <dd class="tabular-nums">{{ money(quote.next_renewal_minor) }} · {{ date(quote.next_renewal_time) }}</dd>
    </dl>
    <p v-if="failure" class="text-destructive text-sm" role="alert">{{ failure }}</p>
  </section>
</template>
