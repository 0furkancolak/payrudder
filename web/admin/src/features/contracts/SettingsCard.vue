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
import { useContractSettings, useSetContractSettings } from "./api";
import { KINDS, RETENTION, requirable, retentionValid, settingsChanged, settingsDraft, withRequired, type Kind } from "./lib";

/**
 * What the seller requires of its customers: which agreements must be accepted before paying, whether a consumer's service starts at once (which
 * needs its own two boxes), and how long the IP address and browser of an acceptance are kept. One form, one Save: the API replaces the first two as a
 * whole. A kind that has no published version cannot be required (the API refuses it too).
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
watch(
  () => settings.data.value,
  (s) => {
    const d = settingsDraft(s);
    required.value = d.required_kinds;
    immediate.value = d.immediate_start;
    retention.value = String(d.retention_months);
  },
  { immediate: true },
);

const months = computed(() => (String(retention.value).trim() === "" ? Number.NaN : Number(retention.value)));
const draft = computed(() => ({ required_kinds: required.value, immediate_start: immediate.value, retention_months: months.value }));
const changed = computed(() => settingsChanged(settingsDraft(settings.data.value), draft.value));
const bad = computed(() => (retentionValid(months.value) ? "" : t("contracts.settings.retentionBad", { min: RETENTION.min, max: RETENTION.max })));
// Saved is what the screen shows once the server's answer is the form: the first edit after it takes it away.
const saved = computed(() => save.isSuccess.value && !changed.value);
const failure = computed(() => firstError(settings, save));

function submit() {
  if (bad.value !== "" || !changed.value) return;
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

        <ErrorText :error="failure" />
        <div class="flex flex-wrap items-center gap-3">
          <Button type="submit" :disabled="!canWrite || !changed || bad !== '' || save.isPending.value" data-testid="contracts-save">{{ t("contracts.settings.save") }}</Button>
          <span v-if="saved" class="text-success text-sm" role="status" data-testid="contracts-saved">{{ t("contracts.settings.saved") }}</span>
        </div>
      </form>
    </Card>
  </fieldset>
</template>
