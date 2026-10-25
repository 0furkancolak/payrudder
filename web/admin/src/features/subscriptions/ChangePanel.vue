<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { type Subscription } from "@/shared/api/client";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Alert } from "@/shared/ui/alert";
import { Button } from "@/shared/ui/button";
import type { Package } from "@/features/packages";
import { useSubscriptionActions, type ChangeInput } from "./api";
import ChangeSelects from "./ChangeSelects.vue";
import { intervalKey } from "./lib";
import QuoteSummary from "./QuoteSummary.vue";

const props = defineProps<{ subscription: Subscription; packages: Package[]; tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const act = useSubscriptionActions(() => props.tenantId);

const plan = ref("");
const interval = ref<ChangeInput["interval"]>("");
const timing = ref<ChangeInput["timing"]>("");
const proration = ref<ChangeInput["proration"]>("");
const input = computed<ChangeInput>(() => ({ id: props.subscription.id ?? "", plan_code: plan.value, interval: interval.value, timing: timing.value, proration: proration.value }));
const nothingChosen = computed(() => plan.value === "" && interval.value === "");
const quote = computed(() => act.previewChange.data.value);
const scheduled = computed(() => props.subscription.scheduled_change);
const date = (v?: string | null) => fmt.date(v, "day", "—");
const intervalName = (v?: string) => t(`billing.subscriptions.${intervalKey(v)}`);
const failure = computed(() => firstError(act.previewChange, act.change, act.cancelScheduledChange));

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
      <ChangeSelects v-model:plan="plan" v-model:interval="interval" v-model:timing="timing" v-model:proration="proration" :packages="packages" show-timing none-key="billing.subscriptions.change.none" @change="reset" />
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" :disabled="nothingChosen || act.previewChange.isPending.value" @click="act.previewChange.mutate(input)">{{ t("billing.subscriptions.change.preview") }}</Button>
      <Button :disabled="nothingChosen || quote === undefined || act.change.isPending.value" @click="apply">{{ t("billing.subscriptions.change.apply") }}</Button>
    </div>
    <QuoteSummary v-if="quote" :quote="quote" :currency="subscription.currency" detailed />
    <ErrorText :error="failure" />
  </section>
</template>
