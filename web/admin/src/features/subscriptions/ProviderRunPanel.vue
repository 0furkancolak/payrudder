<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { type Subscription } from "@/shared/api/client";
import { firstError } from "@/shared/lib/errors";
import type { Package } from "@/features/packages";
import { ErrorText } from "@/shared/ui/error-text";
import { Button } from "@/shared/ui/button";
import { Checkbox } from "@/shared/ui/checkbox";
import { useProviderRunActions, type ChangeInput } from "./api";
import ChangeSelects from "./ChangeSelects.vue";
import QuoteSummary from "./QuoteSummary.vue";

const props = defineProps<{ subscription: Subscription; packages: Package[]; tenantId: string; environment: string }>();
const { t } = useI18n();
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
const failure = computed(() => firstError(...Object.values(act)));
const paymentUrl = computed(() => (act.paymentLink.data.value as { url?: string } | undefined)?.url);
</script>

<template>
  <section class="grid gap-3" role="region" :aria-label="t('billing.subscriptions.providerRun.title', { provider: subscription.provider ?? '' })">
    <h3 class="text-base font-semibold">{{ t("billing.subscriptions.providerRun.title", { provider: subscription.provider ?? "" }) }}</h3>
    <p class="text-muted-foreground text-xs">{{ t("billing.subscriptions.providerRun.hint") }}</p>
    <div class="grid gap-3 sm:grid-cols-3">
      <ChangeSelects v-model:plan="plan" v-model:interval="interval" v-model:proration="proration" :packages="packages" none-key="billing.subscriptions.providerRun.none" @change="act.preview.reset()" />
    </div>
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" :disabled="nothingChosen || act.preview.isPending.value" @click="act.preview.mutate(input)">{{ t("billing.subscriptions.change.preview") }}</Button>
      <Button :disabled="nothingChosen || quote === undefined || act.change.isPending.value" @click="act.change.mutate(input)">{{ t("billing.subscriptions.change.apply") }}</Button>
    </div>
    <QuoteSummary v-if="quote" :quote="quote" :currency="quote.currency ?? subscription.currency" />
    <p v-if="act.change.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.subscriptions.providerRun.changed") }}</p>

    <div class="flex flex-wrap items-center gap-3 border-t pt-3">
      <label class="flex items-center gap-2 text-sm"><Checkbox v-model="atPeriodEnd" :aria-label="t('billing.subscriptions.providerRun.atPeriodEnd')" />{{ t("billing.subscriptions.providerRun.atPeriodEnd") }}</label>
      <Button variant="outline" :disabled="act.pause.isPending.value" @click="act.pause.mutate({ id: subscription.id ?? '', atPeriodEnd })">{{ t("billing.subscriptions.pause") }}</Button>
      <Button variant="outline" :disabled="act.resume.isPending.value" @click="act.resume.mutate(subscription.id ?? '')">{{ t("billing.subscriptions.resume") }}</Button>
      <Button variant="outline" :disabled="act.paymentLink.isPending.value" @click="act.paymentLink.mutate(subscription.id ?? '')">{{ t("billing.subscriptions.providerRun.paymentLink") }}</Button>
    </div>
    <p v-if="act.pause.isSuccess.value || act.resume.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.subscriptions.providerRun.requested") }}</p>
    <p v-if="paymentUrl" class="text-sm" role="status"><a :href="paymentUrl" target="_blank" rel="noopener" class="underline">{{ paymentUrl }}</a></p>
    <ErrorText :error="failure" />
  </section>
</template>
