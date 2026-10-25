<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import PackageSelect from "@/features/packages/PackageSelect.vue";
import type { Package } from "@/features/packages";
import { ROUTABLE_PROVIDERS } from "@/shared/lib/currencies";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import type { SubscriptionActions } from "./api";
import { INTERVALS, type Interval, intervalKey } from "./lib";

/** Starts a subscription: customer, package, currency, interval, trial days, a provider (or the routing rules), and a coupon code. */
const props = defineProps<{ tenantId: string; packages: readonly Package[]; act: SubscriptionActions }>();
const { t } = useI18n();

const customer = ref("");
const plan = ref("");
const currency = ref("");
const trialDays = ref("0");
const provider = ref("");
const coupon = ref("");
const interval = ref<Interval>("SUBSCRIPTION_INTERVAL_MONTH");

function create() {
  props.act.create.mutate(
    {
      customer_external_id: customer.value,
      plan_code: plan.value,
      currency: currency.value || undefined,
      trial_days: Number(trialDays.value) || undefined,
      provider: provider.value || undefined,
      interval: interval.value,
      coupon_code: coupon.value.trim() || undefined,
    },
    { onSuccess: () => ((trialDays.value = "0"), (coupon.value = "")) },
  );
}
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.subscriptions.new") }}</h2>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1fr_1.5fr_0.9fr_1.5fr_1fr]" role="form" :aria-label="t('billing.subscriptions.formLabel')" @submit.prevent="create">
      <CustomerSelect v-model="customer" :tenant-id="tenantId" required lock-placeholder :label="t('billing.subscriptions.customer')" :placeholder="t('billing.subscriptions.customer')" />
      <PackageSelect v-model="plan" :packages="packages" required lock-placeholder :label="t('billing.subscriptions.package')" :placeholder="t('billing.subscriptions.package')" />
      <CurrencySelect v-model="currency" :aria-label="t('billing.subscriptions.currency')" :blank="t('billing.subscriptions.defaultCurrency')" />
      <NativeSelect v-model="interval" :aria-label="t('billing.subscriptions.interval')">
        <option v-for="i in INTERVALS" :key="i" :value="i">{{ t(`billing.subscriptions.${intervalKey(i)}`) }}</option>
      </NativeSelect>
      <Input v-model="trialDays" type="number" min="0" max="365" :aria-label="t('billing.subscriptions.trialDays')" :placeholder="t('billing.subscriptions.trialDays')" />
      <NativeSelect v-model="provider" :aria-label="t('billing.subscriptions.provider')">
        <option value="">{{ t("billing.subscriptions.byRules") }}</option>
        <option v-for="p in ROUTABLE_PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
      </NativeSelect>
      <Input v-model="coupon" :aria-label="t('billing.subscriptions.coupon.code')" :placeholder="t('billing.subscriptions.coupon.code')" />
      <Button type="submit" class="sm:col-span-2 lg:col-span-7" :disabled="act.create.isPending.value">{{ t("billing.subscriptions.create") }}</Button>
    </form>
  </Card>
</template>
