<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { type Subscription } from "@/shared/api/client";
import { useErrorText } from "@/shared/lib/errors";
import type { Package } from "@/features/packages";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { NativeSelect } from "@/shared/ui/native-select";
import { useProviderRunActions, type ChangeInput } from "./api";

const props = defineProps<{ subscription: Subscription; packages: Package[]; tenantId: string; environment: string }>();
const { t } = useI18n();
const fmt = useFormat();
const errorText = useErrorText();
const act = useProviderRunActions(
  () => props.tenantId,
  () => props.environment,
  () => props.subscription.provider ?? "",
);

const plan = ref("");
const interval = ref<ChangeInput["interval"]>("");
const proration = ref<ChangeInput["proration"]>("");
const atPeriodEnd = ref(true);
const input = computed<ChangeInput>(() => ({ id: props.subscription.id ?? "", plan_code: plan.value, interval: interval.value, timing: "", proration: proration.value }));
const nothingChosen = computed(() => plan.value === "" && interval.value === "");
const quote = computed(() => act.preview.data.value);
const money = (v?: string) => fmt.money(v, quote.value?.currency ?? props.subscription.currency);
const outcome = computed(() => {
  const net = quote.value?.net_minor;
  if (net === undefined || net === "0") return t("billing.subscriptions.change.nothing");
  return net.startsWith("-") ? t("billing.subscriptions.change.creditAdded", { amount: money(net.slice(1)) }) : t("billing.subscriptions.change.due", { amount: money(net) });
});
const failure = computed(() => {
  for (const m of Object.values(act)) if (m.error.value) return errorText(m.error.value);
  return "";
});
const paymentUrl = computed(() => (act.paymentLink.data.value as { url?: string } | undefined)?.url);
</script>

<template>
  <section class="grid gap-3" role="region" :aria-label="t('billing.subscriptions.providerRun.title', { provider: subscription.provider ?? '' })">
    <h3 class="text-base font-semibold">{{ t("billing.subscriptions.providerRun.title", { provider: subscription.provider ?? "" }) }}</h3>
    <p class="text-muted-foreground text-xs">{{ t("billing.subscriptions.providerRun.hint") }}</p>
    <div class="grid gap-3 sm:grid-cols-3">
      <NativeSelect v-model="plan" :aria-label="t('billing.subscriptions.change.plan')" @change="act.preview.reset()">
        <option value="">{{ t("billing.subscriptions.change.keepPlan") }}</option>
        <option v-for="p in packages" :key="p.plan.id" :value="p.plan.code">{{ p.plan.code }}</option>
      </NativeSelect>
      <NativeSelect v-model="interval" :aria-label="t('billing.subscriptions.change.interval')" @change="act.preview.reset()">
        <option value="">{{ t("billing.subscriptions.change.keepInterval") }}</option>
        <option value="SUBSCRIPTION_INTERVAL_MONTH">{{ t("billing.subscriptions.monthly") }}</option>
        <option value="SUBSCRIPTION_INTERVAL_YEAR">{{ t("billing.subscriptions.yearly") }}</option>
      </NativeSelect>
      <NativeSelect v-model="proration" :aria-label="t('billing.subscriptions.change.proration')" @change="act.preview.reset()">
        <option value="">{{ t("billing.subscriptions.change.prorate") }}</option>
        <option value="CHANGE_PRORATION_FULL">{{ t("billing.subscriptions.change.full") }}</option>
        <option value="CHANGE_PRORATION_NONE">{{ t("billing.subscriptions.providerRun.none") }}</option>
      </NativeSelect>
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" :disabled="nothingChosen || act.preview.isPending.value" @click="act.preview.mutate(input)">{{ t("billing.subscriptions.change.preview") }}</Button>
      <Button :disabled="nothingChosen || quote === undefined || act.change.isPending.value" @click="act.change.mutate(input)">{{ t("billing.subscriptions.change.apply") }}</Button>
    </div>
    <dl v-if="quote" class="bg-secondary/50 grid gap-1 rounded-2xl px-4 py-3 text-sm sm:grid-cols-2" :aria-label="t('billing.subscriptions.change.quote')">
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.credit") }}</dt>
      <dd class="tabular-nums">{{ money(quote.credit_minor) }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.charge") }}</dt>
      <dd class="tabular-nums">{{ money(quote.charge_minor) }}</dd>
      <dt class="font-medium">{{ t("billing.subscriptions.change.result") }}</dt>
      <dd class="font-medium tabular-nums">{{ outcome }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.subscriptions.change.next") }}</dt>
      <dd class="tabular-nums">{{ money(quote.next_renewal_minor) }}</dd>
    </dl>
    <p v-if="act.change.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.subscriptions.providerRun.changed") }}</p>

    <div class="flex flex-wrap items-center gap-3 border-t pt-3">
      <label class="flex items-center gap-2 text-sm"><Checkbox v-model="atPeriodEnd" :aria-label="t('billing.subscriptions.providerRun.atPeriodEnd')" />{{ t("billing.subscriptions.providerRun.atPeriodEnd") }}</label>
      <Button variant="outline" :disabled="act.pause.isPending.value" @click="act.pause.mutate({ id: subscription.id ?? '', atPeriodEnd })">{{ t("billing.subscriptions.pause") }}</Button>
      <Button variant="outline" :disabled="act.resume.isPending.value" @click="act.resume.mutate(subscription.id ?? '')">{{ t("billing.subscriptions.resume") }}</Button>
      <Button variant="outline" :disabled="act.paymentLink.isPending.value" @click="act.paymentLink.mutate(subscription.id ?? '')">{{ t("billing.subscriptions.providerRun.paymentLink") }}</Button>
    </div>
    <p v-if="act.pause.isSuccess.value || act.resume.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.subscriptions.providerRun.requested") }}</p>
    <p v-if="paymentUrl" class="text-sm" role="status"><a :href="paymentUrl" target="_blank" rel="noopener" class="underline">{{ paymentUrl }}</a></p>
    <p v-if="failure" class="text-destructive text-sm" role="alert">{{ failure }}</p>
  </section>
</template>
