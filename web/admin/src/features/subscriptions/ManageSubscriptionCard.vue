<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import PackageSelect from "@/features/packages/PackageSelect.vue";
import type { Package } from "@/features/packages";
import type { Subscription } from "@/shared/api/client";
import { PROVIDERS, ROUTABLE_PROVIDERS } from "@/shared/lib/currencies";
import type { Environment } from "@/shared/lib/environment";
import { enumWord } from "@/shared/lib/labels";
import { useFormat } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { useMigrateSubscription, type SubscriptionActions } from "./api";
import ChangePanel from "./ChangePanel.vue";
import EntitlementsTable from "./EntitlementsTable.vue";
import { subscriptionAbilities, trialDaysLeft } from "./lib";
import ProviderRunPanel from "./ProviderRunPanel.vue";

/**
 * Everything that can be done with one subscription: pause, resume, cancel at the period end or now, a coupon, a change of package (with a quote when the
 * fee is paid in advance), a move to another payment provider (operators), and the customer's usage against the package's limits. It fills the
 * detail sheet of the subscriptions list, which carries the title.
 */
const props = defineProps<{ tenantId: string; subscription: Subscription; packages: Package[]; environment: Environment; operator: boolean; act: SubscriptionActions }>();
const { t } = useI18n();
const fmt = useFormat();

const can = computed(() => subscriptionAbilities(props.subscription));
const trialLeft = computed(() => trialDaysLeft(props.subscription));
const state = computed(() => enumWord(props.subscription.state, "SUBSCRIPTION_STATE_"));
const id = computed(() => props.subscription.id ?? "");
const cancelLabel = computed(() => (props.subscription.cancel_at_period_end ? "withdrawCancel" : "cancelAtPeriodEnd"));

function end() {
  if (id.value && window.confirm(t("billing.subscriptions.confirmEnd"))) props.act.endNow.mutate(id.value);
}
function endAndCredit() {
  if (id.value && window.confirm(t("billing.subscriptions.confirmEndCredit"))) props.act.endNowCredit.mutate(id.value);
}
const credited = computed(() => (props.act.endNowCredit.data.value as { credited_minor?: string } | undefined)?.credited_minor ?? "");

const couponToApply = ref("");
const newPlan = ref("");

// Moving to another provider is an operator action; a provider that runs subscriptions itself needs the price it should use there.
const migrate = useMigrateSubscription(
  () => props.tenantId,
  () => props.environment,
);
const target = ref("");
const priceRef = ref("");
const targetRunsSubscriptions = computed(() => PROVIDERS.find((p) => p.name === target.value)?.runsSubscriptions ?? false);
const migration = computed(() => migrate.data.value as { state?: string; checkout_url?: string } | undefined);
</script>

<template>
  <div role="region" class="flex flex-col gap-4" :aria-label="t('billing.subscriptions.managing', { customer: subscription.customer_external_id ?? '' })">
    <p v-if="trialLeft !== null" class="bg-muted rounded-2xl px-4 py-3 text-sm" role="status" data-testid="trial-banner">
      {{ t("billing.subscriptions.trialBanner", { date: fmt.date(subscription.trial_end_time, "day"), n: trialLeft }, trialLeft) }}
    </p>
    <p v-if="can.providerRuns" class="text-muted-foreground text-sm">{{ t("billing.subscriptions.providerRuns", { provider: subscription.provider ?? "" }) }}</p>
    <ProviderRunPanel v-if="can.providerRuns && operator && !subscription.end_time" :subscription="subscription" :packages="packages" :tenant-id="tenantId" :environment="environment" />
    <div v-else-if="!can.providerRuns" class="flex flex-wrap gap-2">
      <Button v-if="state === 'active'" variant="outline" :disabled="act.pause.isPending.value" @click="act.pause.mutate(id)">{{ t("billing.subscriptions.pause") }}</Button>
      <Button v-if="state === 'paused'" variant="outline" :disabled="act.resume.isPending.value" @click="act.resume.mutate(id)">{{ t("billing.subscriptions.resume") }}</Button>
      <Button v-if="!subscription.end_time || subscription.cancel_at_period_end" variant="outline" @click="subscription.cancel_at_period_end ? act.withdrawCancel.mutate(id) : act.scheduleCancel.mutate(id)">
        {{ t(`billing.subscriptions.${cancelLabel}`) }}
      </Button>
      <Button v-if="!subscription.end_time" variant="destructive" @click="end">{{ t("billing.subscriptions.endNow") }}</Button>
      <Button v-if="can.canEndWithCredit" variant="destructive" :disabled="act.endNowCredit.isPending.value" @click="endAndCredit">{{ t("billing.subscriptions.endNowCredit") }}</Button>
    </div>
    <p v-if="act.endNowCredit.isSuccess.value" class="text-success text-sm" role="status">
      {{ t("billing.subscriptions.credited", { amount: fmt.money(credited, subscription.currency) }) }}
    </p>

    <div v-if="can.advanceActions" class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.subscriptions.coupon.label')">
      <template v-if="subscription.coupon_code">
        <Badge variant="secondary" class="mono">{{ subscription.coupon_code }}</Badge>
        <Button size="sm" variant="outline" :disabled="act.removeCoupon.isPending.value" @click="act.removeCoupon.mutate(id)">{{ t("billing.subscriptions.coupon.remove") }}</Button>
      </template>
      <template v-else>
        <Input v-model="couponToApply" class="w-48" :aria-label="t('billing.subscriptions.coupon.code')" :placeholder="t('billing.subscriptions.coupon.code')" />
        <Button size="sm" variant="outline" :disabled="couponToApply.trim() === '' || act.applyCoupon.isPending.value" @click="act.applyCoupon.mutate({ id, code: couponToApply.trim() }, { onSuccess: () => (couponToApply = '') })">
          {{ t("billing.subscriptions.coupon.apply") }}
        </Button>
      </template>
    </div>
    <ChangePanel v-if="can.advanceActions" :subscription="subscription" :packages="packages" :tenant-id="tenantId" />
    <div v-else-if="can.manageHere" class="grid gap-3 sm:grid-cols-[1fr_auto]">
      <PackageSelect v-model="newPlan" :packages="packages" lock-placeholder :disabled-code="subscription.plan_code" :label="t('billing.subscriptions.newPackage')" :placeholder="t('billing.subscriptions.newPackage')" />
      <Button variant="outline" :disabled="newPlan === '' || act.changePlan.isPending.value" @click="act.changePlan.mutate({ id, plan_code: newPlan })">
        {{ t("billing.subscriptions.changePlan") }}
      </Button>
      <p class="text-muted-foreground text-xs sm:col-span-2">{{ t("billing.subscriptions.changePlanHint") }}</p>
    </div>

    <div v-if="operator && !subscription.end_time" class="grid gap-3 sm:grid-cols-[1fr_1fr_auto]">
      <NativeSelect v-model="target" :aria-label="t('billing.subscriptions.moveTo')">
        <option value="" disabled>{{ t("billing.subscriptions.moveTo") }}</option>
        <option v-for="p in ROUTABLE_PROVIDERS" :key="p.name" :value="p.name" :disabled="p.name === subscription.provider">{{ p.name }}</option>
      </NativeSelect>
      <Input v-if="targetRunsSubscriptions" v-model="priceRef" :aria-label="t('billing.subscriptions.priceRef')" :placeholder="t('billing.subscriptions.priceRef')" />
      <span v-else />
      <Button
        variant="outline"
        :disabled="target === '' || (targetRunsSubscriptions && priceRef === '') || migrate.isPending.value"
        @click="migrate.mutate({ subscription_id: id, to_provider: target, price_ref: targetRunsSubscriptions ? priceRef : undefined })"
      >
        {{ t("billing.subscriptions.move") }}
      </Button>
      <p class="text-muted-foreground text-xs sm:col-span-3">{{ t("billing.subscriptions.moveHint") }}</p>
    </div>
    <EntitlementsTable :tenant-id="tenantId" :customer="subscription.customer_external_id ?? ''" />
    <ErrorText :error="migrate.error.value" />
    <p v-if="migration?.checkout_url" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status">
      {{ t("billing.subscriptions.checkoutReady") }}
      <a :href="migration.checkout_url" target="_blank" rel="noopener" class="underline">{{ migration.checkout_url }}</a>
    </p>
    <p v-else-if="migration?.state === 'completed'" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status">{{ t("billing.subscriptions.moved") }}</p>
  </div>
</template>
