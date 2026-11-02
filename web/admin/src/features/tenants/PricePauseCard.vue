<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { askConfirm } from "@/shared/lib/confirm";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FormField } from "@/shared/ui/form-field";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Textarea } from "@/shared/ui/textarea";
import { usePriceChangePause, useSetPriceChangePause } from "./api";

/**
 * The operator's kill switch for price changes (plan 15): pause every price change of every seller (no run schedules a subscription, a run in progress stops, the
 * notices already queued wait) and resume them. It is one switch for the platform, with a reason that stays in the audit log.
 */
const { t } = useI18n();
const fmt = useFormat();
const pause = usePriceChangePause();
const set = useSetPriceChangePause();
const paused = computed(() => pause.data.value?.paused === true);
const reason = ref("");

async function toggle() {
  const next = !paused.value;
  if (!(await askConfirm({ body: t(next ? "tenants.pricePause.confirmPause" : "tenants.pricePause.confirmResume"), destructive: next }))) return;
  set.mutate({ paused: next, reason: next ? reason.value.trim() : "" }, { onSuccess: () => (reason.value = "") });
}
</script>

<template>
  <Card class="mb-4 flex flex-col gap-3 p-5" data-testid="price-pause">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="m-0 text-base font-semibold">{{ t("tenants.pricePause.title") }}</h2>
      <Skeleton v-if="pause.isPending.value" class="h-6 w-24 rounded-full" />
      <StatusPill v-else :tone="paused ? 'danger' : 'success'" data-testid="price-pause-state">{{ t(paused ? "tenants.pricePause.paused" : "tenants.pricePause.running") }}</StatusPill>
    </div>
    <p class="text-muted-foreground m-0 text-sm">{{ t("tenants.pricePause.hint") }}</p>
    <p v-if="paused" class="m-0 text-sm" data-testid="price-pause-by">
      {{ t("tenants.pricePause.by", { who: pause.data.value?.updated_by ?? "", date: fmt.date(pause.data.value?.update_time, "dateTime") }) }}
      <span v-if="pause.data.value?.reason" class="text-muted-foreground break-words" dir="auto"> · {{ pause.data.value.reason }}</span>
    </p>
    <ErrorState :error="pause.error.value" :retrying="pause.isFetching.value" compact @retry="pause.refetch()" />
    <ErrorText :error="set.error.value" />
    <form v-if="pause.data.value" class="flex flex-col gap-3" @submit.prevent="toggle">
      <FormField v-if="!paused" :label="t('tenants.pricePause.reason')" for="pause-reason" :hint="t('tenants.pricePause.reasonHint')">
        <Textarea id="pause-reason" v-model="reason" maxlength="500" rows="2" data-testid="price-pause-reason" />
      </FormField>
      <div>
        <Button type="submit" :variant="paused ? 'default' : 'outline'" :disabled="set.isPending.value" data-testid="price-pause-toggle">{{ t(paused ? "tenants.pricePause.resume" : "tenants.pricePause.pause") }}</Button>
      </div>
    </form>
  </Card>
</template>
