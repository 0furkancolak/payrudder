<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { firstError } from "@/shared/lib/errors";
import { useCanWrite } from "@/shared/lib/access";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Checkbox } from "@/shared/ui/checkbox";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Switch } from "@/shared/ui/switch";
import { Textarea } from "@/shared/ui/textarea";
import { useContractSettings, useSetContractSettings } from "./api";
import { CANCEL_TEXT_MAX, KINDS, LANGUAGES, LANGUAGE_NAMES, RENEWAL, RETENTION, renewalDaysValid, requirable, retentionValid, settingsChanged, settingsDraft, withRequired, type Kind, type Refund } from "./lib";

/**
 * What the seller requires of its customers: which agreements must be accepted before paying, whether a consumer's service starts at once (which
 * needs its own two boxes), how long the IP address and browser of an acceptance are kept, what accepting a withdrawal does about the money, whether
 * customers can withdraw on the payment pages, and the renewal notice (days ahead and the seller's own words on how to cancel). One form, one Save: the
 * API replaces the required kinds and the switch as a whole. A kind that has no published version cannot be required (the API refuses it too).
 */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const canWrite = useCanWrite();
const settings = useContractSettings(() => props.tenantId);
const save = useSetContractSettings(() => props.tenantId);

const required = ref<Kind[]>([]);
const immediate = ref(false);
// A number input gives its model a number once it holds one, so the model is either.
const retention = ref<string | number>(String(RETENTION.default));
const refund = ref<Refund>("full");
const withdrawalFunction = ref(true);
const noticeDays = ref<string | number>(String(RENEWAL.default));
const cancelTexts = ref(settingsDraft(undefined).renewal_cancel_texts);
watch(
  () => settings.data.value,
  (s) => {
    const d = settingsDraft(s);
    required.value = d.required_kinds;
    immediate.value = d.immediate_start;
    retention.value = String(d.retention_months);
    refund.value = d.withdrawal_refund;
    withdrawalFunction.value = d.withdrawal_function;
    noticeDays.value = String(d.renewal_notice_days);
    cancelTexts.value = { ...d.renewal_cancel_texts };
  },
  { immediate: true },
);

const months = computed(() => (String(retention.value).trim() === "" ? Number.NaN : Number(retention.value)));
const days = computed(() => (String(noticeDays.value).trim() === "" ? Number.NaN : Number(noticeDays.value)));
const draft = computed(() => ({
  required_kinds: required.value,
  immediate_start: immediate.value,
  retention_months: months.value,
  withdrawal_refund: refund.value,
  renewal_notice_days: days.value,
  renewal_cancel_texts: cancelTexts.value,
  withdrawal_function: withdrawalFunction.value,
}));
const changed = computed(() => settingsChanged(settingsDraft(settings.data.value), draft.value));
const bad = computed(() => (retentionValid(months.value) ? "" : t("contracts.settings.retentionBad", { min: RETENTION.min, max: RETENTION.max })));
const badDays = computed(() => (renewalDaysValid(days.value) ? "" : t("contracts.settings.noticeBad", { min: RENEWAL.min, max: RENEWAL.max })));
// Saved is what the screen shows once the server's answer is the form: the first edit after it takes it away.
const saved = computed(() => save.isSuccess.value && !changed.value);
const failure = computed(() => firstError(settings, save));

function submit() {
  if (bad.value !== "" || badDays.value !== "" || !changed.value) return;
  save.mutate(draft.value);
}
</script>

<template>
  <fieldset :disabled="!canWrite" class="m-0 min-w-0 border-0 p-0">
    <Card class="gap-6" data-testid="contracts-settings">
      <ErrorState :error="settings.error.value" :retrying="settings.isFetching.value" compact @retry="settings.refetch()" />
      <form class="contents" novalidate @submit.prevent="submit">
        <section class="grid gap-3" aria-labelledby="contracts-required-title">
          <div>
            <h2 id="contracts-required-title" class="text-lg font-semibold">{{ t("contracts.settings.requiredTitle") }}</h2>
            <p class="text-muted-foreground text-sm">{{ t("contracts.settings.requiredHint") }}</p>
          </div>
          <ul class="divide-y">
            <li v-for="k in KINDS" :key="k" class="flex items-start gap-3 py-3">
              <Checkbox
                :id="`required-${k}`"
                class="mt-0.5"
                :model-value="required.includes(k)"
                :disabled="!requirable(k, settings.data.value)"
                :data-testid="`required-${k}`"
                @update:model-value="required = withRequired(required, k, $event === true)"
              />
              <label :for="`required-${k}`" class="min-w-0 flex-1 cursor-pointer">
                <span class="block text-sm font-medium">{{ t(`contracts.kinds.${k}`) }}</span>
                <span class="text-muted-foreground block text-xs">{{ t(`contracts.settings.kindHints.${k}`) }}</span>
                <span v-if="!requirable(k, settings.data.value) && !settings.isPending.value" class="text-warning block text-xs" :data-testid="`required-${k}-reason`">{{ t("contracts.settings.unpublished") }}</span>
              </label>
            </li>
          </ul>
        </section>

        <section class="flex items-start justify-between gap-4">
          <label for="contracts-immediate" class="min-w-0 flex-1 cursor-pointer">
            <span class="block text-lg font-semibold">{{ t("contracts.settings.immediateTitle") }}</span>
            <span class="text-muted-foreground block text-sm">{{ t("contracts.settings.immediateHint") }}</span>
            <ul class="text-muted-foreground mt-2 list-disc ps-5 text-sm">
              <li>{{ t("contracts.settings.boxStart") }}</li>
              <li>{{ t("contracts.settings.boxWaiver") }}</li>
            </ul>
          </label>
          <Switch id="contracts-immediate" v-model="immediate" data-testid="contracts-immediate" />
        </section>

        <section class="grid gap-1.5">
          <label for="contracts-retention" class="text-lg font-semibold">{{ t("contracts.settings.retentionTitle") }}</label>
          <p class="text-muted-foreground text-sm">{{ t("contracts.settings.retentionHint", { min: RETENTION.min, max: RETENTION.max, default: RETENTION.default }) }}</p>
          <div class="flex items-center gap-2">
            <Input id="contracts-retention" v-model="retention" type="number" inputmode="numeric" :min="RETENTION.min" :max="RETENTION.max" class="max-w-28" :aria-invalid="bad ? true : undefined" aria-describedby="contracts-retention-error" data-testid="contracts-retention" />
            <span class="text-muted-foreground text-sm">{{ t("contracts.settings.months") }}</span>
          </div>
          <p v-if="bad" id="contracts-retention-error" class="text-destructive text-xs" role="alert">{{ bad }}</p>
        </section>

        <section class="grid gap-3" aria-labelledby="contracts-withdrawal-title">
          <div>
            <h2 id="contracts-withdrawal-title" class="text-lg font-semibold">{{ t("contracts.settings.withdrawalTitle") }}</h2>
            <p class="text-muted-foreground text-sm">{{ t("contracts.settings.withdrawalHint") }}</p>
          </div>
          <div class="flex items-start justify-between gap-4">
            <label for="contracts-refund" class="min-w-0 flex-1 cursor-pointer">
              <span class="block text-sm font-medium">{{ t("contracts.settings.refundTitle") }}</span>
              <span class="text-muted-foreground block text-xs">{{ t("contracts.settings.refundHint") }}</span>
            </label>
            <Switch id="contracts-refund" :model-value="refund === 'full'" data-testid="contracts-refund" @update:model-value="refund = $event ? 'full' : 'none'" />
          </div>
          <div class="flex items-start justify-between gap-4">
            <label for="contracts-function" class="min-w-0 flex-1 cursor-pointer">
              <span class="block text-sm font-medium">{{ t("contracts.settings.functionTitle") }}</span>
              <span class="text-muted-foreground block text-xs">{{ t("contracts.settings.functionHint") }}</span>
            </label>
            <Switch id="contracts-function" v-model="withdrawalFunction" data-testid="contracts-function" />
          </div>
        </section>

        <section class="grid gap-3" aria-labelledby="contracts-notice-title">
          <div>
            <h2 id="contracts-notice-title" class="text-lg font-semibold">{{ t("contracts.settings.noticeTitle") }}</h2>
            <p class="text-muted-foreground text-sm">{{ t("contracts.settings.noticeHint", { default: RENEWAL.default, max: RENEWAL.max }) }}</p>
          </div>
          <div class="flex items-center gap-2">
            <Input id="contracts-notice-days" v-model="noticeDays" type="number" inputmode="numeric" :min="RENEWAL.min" :max="RENEWAL.max" class="max-w-28" :aria-label="t('contracts.settings.noticeTitle')" :aria-invalid="badDays ? true : undefined" aria-describedby="contracts-notice-error" data-testid="contracts-notice-days" />
            <span class="text-muted-foreground text-sm">{{ t("contracts.settings.noticeDays") }}</span>
          </div>
          <p v-if="badDays" id="contracts-notice-error" class="text-destructive text-xs" role="alert">{{ badDays }}</p>
          <div>
            <h3 class="text-sm font-medium">{{ t("contracts.settings.cancelTitle") }}</h3>
            <p class="text-muted-foreground text-xs">{{ t("contracts.settings.cancelHint") }}</p>
          </div>
          <div v-for="l in LANGUAGES" :key="l" class="grid gap-1">
            <label :for="`contracts-cancel-${l}`" class="text-muted-foreground text-xs font-medium">{{ LANGUAGE_NAMES[l] }}</label>
            <Textarea :id="`contracts-cancel-${l}`" v-model="cancelTexts[l]" :maxlength="CANCEL_TEXT_MAX" :dir="l === 'ar' ? 'rtl' : 'ltr'" rows="2" :data-testid="`contracts-cancel-${l}`" />
          </div>
        </section>

        <ErrorText :error="failure" />
        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="!canWrite || !changed || bad !== '' || badDays !== '' || save.isPending.value" data-testid="contracts-save">{{ t("contracts.settings.save") }}</Button>
          <span v-if="saved" class="text-success text-sm" role="status" data-testid="contracts-saved">{{ t("contracts.settings.saved") }}</span>
        </div>
      </form>
    </Card>
  </fieldset>
</template>
