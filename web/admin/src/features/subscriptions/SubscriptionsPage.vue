<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError, type Subscription } from "@/shared/api/client";
import { CURRENCIES, PROVIDERS } from "@/shared/lib/currencies";
import { enumWord, label } from "@/shared/lib/labels";
import { useCustomers } from "@/features/customers";
import { usePackages } from "@/features/packages";
import { useTenant } from "@/features/tenants";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useEntitlements, useSubscriptionActions, useSubscriptions } from "./api";
import ChangePanel from "./ChangePanel.vue";
import ProviderRunPanel from "./ProviderRunPanel.vue";

const { t, te, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);
const environment = computed(() => (tenant.data.value?.environment === "ENVIRONMENT_SANDBOX" ? "sandbox" : "live"));
const subs = useSubscriptions(tenantId);
const customers = useCustomers(tenantId);
const packages = usePackages(tenantId);
const act = useSubscriptionActions(tenantId, environment);

const customer = ref("");
const plan = ref("");
const currency = ref("");
const trialDays = ref("0");
const provider = ref("");
const coupon = ref("");
const couponToApply = ref("");
const interval = ref<"SUBSCRIPTION_INTERVAL_MONTH" | "SUBSCRIPTION_INTERVAL_YEAR">("SUBSCRIPTION_INTERVAL_MONTH");
function create() {
  act.create.mutate(
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

const selectedId = ref("");
const usageCustomer = computed(() => (subs.data.value?.subscriptions ?? []).find((s) => s.id === selectedId.value)?.customer_external_id ?? "");
const entitlements = useEntitlements(tenantId, usageCustomer);
/** How much of the limit is used, for the bar: a display value only, the exact decimals are what is written. */
const percent = (used?: string, limit?: string | null) => (limit && Number(limit) > 0 ? Math.min(100, Math.round((Number(used ?? 0) / Number(limit)) * 100)) : 0);
const selected = computed<Subscription | undefined>(() => (subs.data.value?.subscriptions ?? []).find((s) => s.id === selectedId.value));
const state = (s: Subscription) => enumWord(s.state, "SUBSCRIPTION_STATE_");
const providerRuns = computed(() => selected.value?.owner === "provider");
const newPlan = ref("");
const target = ref("");
const priceRef = ref("");
const targetRunsSubscriptions = computed(() => PROVIDERS.find((p) => p.name === target.value)?.runsSubscriptions ?? false);
const migration = computed(() => act.migrate.data.value as { state?: string; checkout_url?: string } | undefined);

function end(id: string | undefined) {
  if (id && window.confirm(t("billing.subscriptions.confirmEnd"))) act.endNow.mutate(id);
}
const failure = computed(() => {
  for (const m of Object.values(act)) if (m.error.value) return toApiError(m.error.value).message;
  return "";
});
const date = (v?: string | null) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "—");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.subscriptions.new") }}</h2>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1fr_1.5fr_0.9fr_1.5fr_1fr]" role="form" :aria-label="t('billing.subscriptions.formLabel')" @submit.prevent="create">
      <NativeSelect v-model="customer" :aria-label="t('billing.subscriptions.customer')" required>
        <option value="" disabled>{{ t("billing.subscriptions.customer") }}</option>
        <option v-for="c in customers.data.value?.customers ?? []" :key="c.id" :value="c.external_id">{{ c.external_id }}</option>
      </NativeSelect>
      <NativeSelect v-model="plan" :aria-label="t('billing.subscriptions.package')" required>
        <option value="" disabled>{{ t("billing.subscriptions.package") }}</option>
        <option v-for="p in packages.data.value ?? []" :key="p.plan.id" :value="p.plan.code">{{ p.plan.code }}</option>
      </NativeSelect>
      <NativeSelect v-model="currency" :aria-label="t('billing.subscriptions.currency')">
        <option value="">{{ t("billing.subscriptions.defaultCurrency") }}</option>
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <NativeSelect v-model="interval" :aria-label="t('billing.subscriptions.interval')">
        <option value="SUBSCRIPTION_INTERVAL_MONTH">{{ t("billing.subscriptions.monthly") }}</option>
        <option value="SUBSCRIPTION_INTERVAL_YEAR">{{ t("billing.subscriptions.yearly") }}</option>
      </NativeSelect>
      <Input v-model="trialDays" type="number" min="0" max="365" :aria-label="t('billing.subscriptions.trialDays')" :placeholder="t('billing.subscriptions.trialDays')" />
      <NativeSelect v-model="provider" :aria-label="t('billing.subscriptions.provider')">
        <option value="">{{ t("billing.subscriptions.byRules") }}</option>
        <option v-for="p in PROVIDERS" :key="p.name" :value="p.name">{{ p.name }}</option>
      </NativeSelect>
      <Input v-model="coupon" :aria-label="t('billing.subscriptions.coupon.code')" :placeholder="t('billing.subscriptions.coupon.code')" />
      <Button type="submit" class="sm:col-span-2 lg:col-span-7" :disabled="act.create.isPending.value">{{ t("billing.subscriptions.create") }}</Button>
    </form>
  </Card>
  <p v-if="failure" class="text-destructive bg-destructive/10 mb-4 rounded-2xl px-4 py-3 text-sm" role="alert">{{ failure }}</p>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.tabs.subscriptions") }}</h2>
    <p v-if="subs.error.value" class="text-destructive px-3 text-sm" role="alert">{{ toApiError(subs.error.value).message }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.subscriptions.customer") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.package") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.interval") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.currency") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.provider") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.since") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.until") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="s in subs.data.value?.subscriptions ?? []" :key="s.id" :data-state="s.id === selectedId ? 'selected' : undefined">
          <TableCell class="font-medium">{{ s.customer_external_id }}</TableCell>
          <TableCell class="mono">{{ s.plan_code }} v{{ s.plan_version }}</TableCell>
          <TableCell>
            <Badge :variant="state(s) === 'active' ? 'success' : state(s) === 'cancelled' || state(s) === 'past_due' ? 'destructive' : 'secondary'">
              {{ label(t, te, "subscriptionState", state(s)) }}
            </Badge>
            <span v-if="s.cancel_at_period_end" class="text-muted-foreground ms-2 text-xs">{{ t("billing.subscriptions.endsWithPeriod") }}</span>
          </TableCell>
          <TableCell>{{ s.interval === "SUBSCRIPTION_INTERVAL_YEAR" ? t("billing.subscriptions.yearly") : t("billing.subscriptions.monthly") }}</TableCell>
          <TableCell>{{ s.currency }}</TableCell>
          <TableCell>
            {{ s.provider || "—" }}
            <Badge v-if="s.owner === 'provider'" variant="secondary" class="ms-1">{{ t("billing.subscriptions.runByProvider") }}</Badge>
          </TableCell>
          <TableCell>{{ date(s.start_time) }}</TableCell>
          <TableCell>{{ date(s.end_time) }}</TableCell>
          <TableCell class="text-end"><Button size="sm" variant="outline" @click="selectedId = s.id ?? ''">{{ t("billing.subscriptions.manage") }}</Button></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!subs.isPending.value && (subs.data.value?.subscriptions ?? []).length === 0 && !subs.error.value" class="text-muted-foreground p-4 text-center text-sm">
      {{ t("billing.subscriptions.empty") }}
    </p>
  </Card>

  <Card v-if="selected" role="region" :aria-label="t('billing.subscriptions.managing', { customer: selected.customer_external_id ?? '' })">
    <h2 class="text-lg font-semibold">{{ t("billing.subscriptions.managing", { customer: selected.customer_external_id ?? "" }) }}</h2>
    <p v-if="providerRuns" class="text-muted-foreground text-sm">{{ t("billing.subscriptions.providerRuns", { provider: selected.provider ?? "" }) }}</p>
    <ProviderRunPanel v-if="providerRuns && !selected.end_time" :subscription="selected" :packages="packages.data.value ?? []" :tenant-id="tenantId" :environment="environment" />
    <div v-else class="flex flex-wrap gap-2">
      <Button v-if="state(selected) === 'active'" variant="outline" :disabled="act.pause.isPending.value" @click="act.pause.mutate(selected.id ?? '')">{{ t("billing.subscriptions.pause") }}</Button>
      <Button v-if="state(selected) === 'paused'" variant="outline" :disabled="act.resume.isPending.value" @click="act.resume.mutate(selected.id ?? '')">{{ t("billing.subscriptions.resume") }}</Button>
      <Button v-if="!selected.cancel_at_period_end && !selected.end_time" variant="outline" @click="act.scheduleCancel.mutate(selected.id ?? '')">{{ t("billing.subscriptions.cancelAtPeriodEnd") }}</Button>
      <Button v-if="selected.cancel_at_period_end" variant="outline" @click="act.withdrawCancel.mutate(selected.id ?? '')">{{ t("billing.subscriptions.withdrawCancel") }}</Button>
      <Button v-if="!selected.end_time" variant="destructive" @click="end(selected.id)">{{ t("billing.subscriptions.endNow") }}</Button>
    </div>

    <div v-if="!providerRuns && !selected.end_time && selected.fee_timing === 'FEE_TIMING_ADVANCE'" class="flex flex-wrap items-center gap-2" role="group" :aria-label="t('billing.subscriptions.coupon.label')">
      <template v-if="selected.coupon_code">
        <Badge variant="secondary" class="mono">{{ selected.coupon_code }}</Badge>
        <Button size="sm" variant="outline" :disabled="act.removeCoupon.isPending.value" @click="act.removeCoupon.mutate(selected.id ?? '')">{{ t("billing.subscriptions.coupon.remove") }}</Button>
      </template>
      <template v-else>
        <Input v-model="couponToApply" class="w-48" :aria-label="t('billing.subscriptions.coupon.code')" :placeholder="t('billing.subscriptions.coupon.code')" />
        <Button size="sm" variant="outline" :disabled="couponToApply.trim() === '' || act.applyCoupon.isPending.value" @click="act.applyCoupon.mutate({ id: selected.id ?? '', code: couponToApply.trim() }, { onSuccess: () => (couponToApply = '') })">
          {{ t("billing.subscriptions.coupon.apply") }}
        </Button>
      </template>
    </div>
    <ChangePanel
      v-if="!providerRuns && !selected.end_time && selected.fee_timing === 'FEE_TIMING_ADVANCE'"
      :subscription="selected"
      :packages="packages.data.value ?? []"
      :tenant-id="tenantId"
      :environment="environment"
    />
    <div v-else-if="!providerRuns && !selected.end_time" class="grid gap-3 sm:grid-cols-[1fr_auto]">
      <NativeSelect v-model="newPlan" :aria-label="t('billing.subscriptions.newPackage')">
        <option value="" disabled>{{ t("billing.subscriptions.newPackage") }}</option>
        <option v-for="p in packages.data.value ?? []" :key="p.plan.id" :value="p.plan.code" :disabled="p.plan.code === selected.plan_code">{{ p.plan.code }}</option>
      </NativeSelect>
      <Button variant="outline" :disabled="newPlan === '' || act.changePlan.isPending.value" @click="act.changePlan.mutate({ id: selected.id ?? '', plan_code: newPlan })">
        {{ t("billing.subscriptions.changePlan") }}
      </Button>
      <p class="text-muted-foreground text-xs sm:col-span-2">{{ t("billing.subscriptions.changePlanHint") }}</p>
    </div>

    <div v-if="!selected.end_time" class="grid gap-3 sm:grid-cols-[1fr_1fr_auto]">
      <NativeSelect v-model="target" :aria-label="t('billing.subscriptions.moveTo')">
        <option value="" disabled>{{ t("billing.subscriptions.moveTo") }}</option>
        <option v-for="p in PROVIDERS" :key="p.name" :value="p.name" :disabled="p.name === selected.provider">{{ p.name }}</option>
      </NativeSelect>
      <Input v-if="targetRunsSubscriptions" v-model="priceRef" :aria-label="t('billing.subscriptions.priceRef')" :placeholder="t('billing.subscriptions.priceRef')" />
      <span v-else />
      <Button
        variant="outline"
        :disabled="target === '' || (targetRunsSubscriptions && priceRef === '') || act.migrate.isPending.value"
        @click="act.migrate.mutate({ subscription_id: selected.id ?? '', to_provider: target, price_ref: targetRunsSubscriptions ? priceRef : undefined })"
      >
        {{ t("billing.subscriptions.move") }}
      </Button>
      <p class="text-muted-foreground text-xs sm:col-span-3">{{ t("billing.subscriptions.moveHint") }}</p>
    </div>
    <div role="region" :aria-label="t('billing.subscriptions.usage')" class="grid gap-2">
      <h3 class="text-base font-semibold">{{ t("billing.subscriptions.usage") }}</h3>
      <p v-if="entitlements.error.value" class="text-destructive text-sm" role="alert">{{ toApiError(entitlements.error.value).message }}</p>
      <Table v-if="(entitlements.data.value?.items ?? []).length > 0">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.subscriptions.meter") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.subscriptions.used") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.subscriptions.limit") }}</TableHead>
            <TableHead class="w-40" />
            <TableHead>{{ t("billing.subscriptions.allowed") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="e in entitlements.data.value?.items ?? []" :key="e.meter_code">
            <TableCell class="mono">{{ e.meter_code }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ e.used }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ e.unlimited ? t("billing.subscriptions.unlimited") : e.limit }}</TableCell>
            <TableCell>
              <div v-if="!e.unlimited" class="bg-secondary h-2 overflow-hidden rounded-full" role="progressbar" :aria-valuenow="percent(e.used, e.limit)" aria-valuemin="0" aria-valuemax="100">
                <div class="h-full rounded-full" :class="e.allowed ? 'bg-primary' : 'bg-destructive'" :style="{ width: `${percent(e.used, e.limit)}%` }" />
              </div>
            </TableCell>
            <TableCell><Badge :variant="e.allowed ? 'success' : 'destructive'">{{ e.allowed ? t("billing.subscriptions.withinLimit") : t("billing.subscriptions.overLimit") }}</Badge></TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-else-if="!entitlements.isPending.value && !entitlements.error.value" class="text-muted-foreground text-sm">{{ t("billing.subscriptions.noLimits") }}</p>
    </div>
    <p v-if="migration?.checkout_url" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status">
      {{ t("billing.subscriptions.checkoutReady") }}
      <a :href="migration.checkout_url" target="_blank" rel="noopener" class="underline">{{ migration.checkout_url }}</a>
    </p>
    <p v-else-if="migration?.state === 'completed'" class="bg-success/10 text-success rounded-2xl px-4 py-3 text-sm" role="status">{{ t("billing.subscriptions.moved") }}</p>
  </Card>
</template>
