<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { type Subscription } from "@/shared/api/client";
import { firstError } from "@/shared/lib/errors";
import { ErrorText } from "@/shared/ui/error-text";
import { Alert } from "@/shared/ui/alert";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import type { Package } from "@/features/packages";
import { useSubscriptionActions, type ChangeInput } from "./api";
import ChangeSelects from "./ChangeSelects.vue";
import { intervalKey, MAX_SEATS, parseSeats, seatsOf } from "./lib";
import QuoteSummary from "./QuoteSummary.vue";

const props = defineProps<{ subscription: Subscription; packages: Package[]; tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();
const act = useSubscriptionActions(() => props.tenantId);

const plan = ref("");
const interval = ref<ChangeInput["interval"]>("");
const timing = ref<ChangeInput["timing"]>("");
const proration = ref<ChangeInput["proration"]>("");
const seats = ref("");
const current = computed(() => seatsOf(props.subscription));
const parsedSeats = computed(() => parseSeats(seats.value));
/** The seats are a change only when they are a valid number other than the ones the subscription has. */
const newSeats = computed(() => (typeof parsedSeats.value === "number" && parsedSeats.value !== current.value ? parsedSeats.value : null));
const input = computed<ChangeInput>(() => ({ id: props.subscription.id ?? "", plan_code: plan.value, interval: interval.value, timing: timing.value, proration: proration.value, quantity: newSeats.value }));
const nothingChosen = computed(() => parsedSeats.value === "bad" || (plan.value === "" && interval.value === "" && newSeats.value === null));
const quote = computed(() => act.previewChange.data.value);
const scheduled = computed(() => props.subscription.scheduled_change);
const date = (v?: string | null) => fmt.date(v, "day", "—");
const intervalName = (v?: string) => t(`billing.subscriptions.${intervalKey(v)}`);
const scheduledSeats = computed(() => (scheduled.value?.quantity && scheduled.value.quantity !== current.value ? scheduled.value.quantity : null));
const failure = computed(() => firstError(act.previewChange, act.change, act.cancelScheduledChange));

function reset() {
  act.previewChange.reset();
}
function apply() {
  act.change.mutate(input.value, {
    onSuccess: () => {
      plan.value = interval.value = timing.value = proration.value = seats.value = "";
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
          {{ scheduledSeats === null ? t("billing.subscriptions.change.scheduled", { plan: scheduled.plan_code ?? "", interval: intervalName(scheduled.interval), date: date(scheduled.effective_time) }) : t("billing.subscriptions.change.scheduledSeats", { plan: scheduled.plan_code ?? "", interval: intervalName(scheduled.interval), seats: scheduledSeats, date: date(scheduled.effective_time) }) }}
        </span>
        <Button size="sm" variant="outline" :disabled="act.cancelScheduledChange.isPending.value" @click="act.cancelScheduledChange.mutate(subscription.id ?? '')">
          {{ t("billing.subscriptions.change.withdraw") }}
        </Button>
      </div>
    </Alert>
    <div class="grid gap-3 sm:grid-cols-2">
      <ChangeSelects v-model:plan="plan" v-model:interval="interval" v-model:timing="timing" v-model:proration="proration" :packages="packages" show-timing none-key="billing.subscriptions.change.none" @change="reset" />
    </div>
    <div class="grid gap-1.5 sm:max-w-xs">
      <Input
        v-model="seats"
        type="number"
        min="1"
        :max="MAX_SEATS"
        inputmode="numeric"
        :aria-label="t('billing.subscriptions.change.seats')"
        :placeholder="t('billing.subscriptions.change.keepSeats', { n: current })"
        data-testid="change-seats"
        @input="reset"
      />
      <p v-if="parsedSeats === 'bad'" class="text-destructive text-xs" role="alert">{{ t("billing.subscriptions.seatsBad", { max: MAX_SEATS }) }}</p>
      <p v-else class="text-muted-foreground text-xs">{{ t("billing.subscriptions.change.seatsHint") }}</p>
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" :disabled="nothingChosen || act.previewChange.isPending.value" @click="act.previewChange.mutate(input)">{{ t("billing.subscriptions.change.preview") }}</Button>
      <Button :disabled="nothingChosen || quote === undefined || act.change.isPending.value" @click="apply">{{ t("billing.subscriptions.change.apply") }}</Button>
    </div>
    <p v-if="nothingChosen && parsedSeats !== 'bad'" class="text-muted-foreground text-xs" data-testid="change-preview-hint">{{ t("billing.subscriptions.change.previewHint") }}</p>
    <QuoteSummary v-if="quote" :quote="quote" :currency="subscription.currency" detailed />
    <ErrorText :error="failure" />
  </section>
</template>
