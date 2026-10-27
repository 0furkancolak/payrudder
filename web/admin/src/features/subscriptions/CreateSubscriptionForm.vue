<script setup lang="ts">
import { ref, watch } from "vue";
import AppField from "@/app/layout/AppField.vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import PackageSelect from "@/features/packages/PackageSelect.vue";
import type { Package } from "@/features/packages";
import { ROUTABLE_PROVIDERS } from "@/shared/lib/currencies";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import type { SubscriptionActions } from "./api";
import { INTERVALS, type Interval, intervalKey } from "./lib";

/**
 * Starts a subscription: customer, package, currency, interval, trial days, a provider (or the routing rules), and a coupon code. `forCustomer` picks
 * the customer in advance (the form opened from a customer's sheet).
 */
const props = defineProps<{ tenantId: string; packages: readonly Package[]; act: SubscriptionActions; forCustomer?: string }>();
const { t } = useI18n();
const open = defineModel<boolean>("open", { default: false });

const customer = ref("");
const plan = ref("");
const currency = ref("");
const trialDays = ref("0");
const provider = ref("");
const coupon = ref("");
const interval = ref<Interval>("SUBSCRIPTION_INTERVAL_MONTH");

// A sheet that was closed opens clean.
watch(open, (v) => {
  if (v) {
    plan.value = currency.value = provider.value = coupon.value = "";
    customer.value = props.forCustomer ?? "";
    trialDays.value = "0";
    interval.value = "SUBSCRIPTION_INTERVAL_MONTH";
    props.act.create.reset();
  }
});

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
    { onSuccess: () => ((trialDays.value = "0"), (coupon.value = ""), (open.value = false)) },
  );
}
</script>

<template>
  <Sheet v-model:open="open" name="new-subscription">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.subscriptions.new") }}</SheetTitle>
        <SheetDescription>{{ t("billing.tabs.subscriptions") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="create-subscription" class="grid gap-3" role="form" :aria-label="t('billing.subscriptions.formLabel')" @submit.prevent="create">
          <AppField />
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
        </form>
      </SheetBody>
      <SheetFooter>
        <Button type="button" variant="ghost" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="create-subscription" :disabled="act.create.isPending.value">{{ t("billing.subscriptions.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
