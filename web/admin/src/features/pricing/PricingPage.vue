<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { COUNTRY_HINTS } from "@/shared/lib/currencies";
import { useCanWrite } from "@/shared/lib/access";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { parsePercent, useDunningSettings, useSetDunningSettings, useSetTaxRates, useTaxRates, type Rate } from "./api";
import CouponsCard from "./CouponsCard.vue";
import RedemptionsCard from "./RedemptionsCard.vue";

const { t } = useI18n();
const canWrite = useCanWrite();
const errorText = useErrorText();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// --- tax
const stored = useTaxRates(tenantId);
const setRates = useSetTaxRates(tenantId);
const rates = ref<Rate[]>([]);
const taxError = ref("");
watch(
  () => stored.data.value,
  (v) => (rates.value = (v ?? []).map((r) => ({ ...r }))),
  { immediate: true },
);
function saveRates() {
  taxError.value = "";
  const out: { country: string; name: string; rate_bps: number }[] = [];
  for (const r of rates.value) {
    const bps = parsePercent(r.percent);
    if (bps === null) {
      taxError.value = t("billing.pricing.tax.badRate");
      return;
    }
    out.push({ country: r.country.trim().toUpperCase(), name: r.name.trim(), rate_bps: bps });
  }
  setRates.mutate(out);
}

// --- payment retries
const dunningStored = useDunningSettings(tenantId);
const setDunning = useSetDunningSettings(tenantId);
const retryHours = ref<string[]>([]);
const dunningError = ref("");
watch(
  () => dunningStored.data.value,
  (v) => (retryHours.value = (v?.retry_hours ?? []).map(String)),
  { immediate: true },
);
function saveDunning() {
  dunningError.value = "";
  const out = retryHours.value.map((h) => (/^\d{1,3}$/.test(h.trim()) ? Number(h) : NaN));
  if (out.some((n) => !(n >= 1 && n <= 720))) {
    dunningError.value = t("billing.pricing.dunning.bad");
    return;
  }
  setDunning.mutate(out);
}
</script>

<template>
  <!-- A viewer reads the promotions, the retry schedule and the tax rates; the controls that change them are switched off (the server refuses them anyway). -->
  <fieldset :disabled="!canWrite" class="m-0 min-w-0 border-0 p-0">
    <CouponsCard :tenant-id="tenantId" />
    <RedemptionsCard :tenant-id="tenantId" />

    <Card class="mb-6">
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.dunning.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.hint") }}</p>
      <p v-if="dunningStored.data.value?.defaults_in_use" class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.defaults") }}</p>
      <p v-if="dunningStored.error.value" class="text-destructive text-sm" role="alert">{{ errorText(dunningStored.error.value) }}</p>
      <div v-for="(_, i) in retryHours" :key="i" class="flex items-center gap-3" role="group" :aria-label="t('billing.pricing.dunning.retry', { n: i + 1 })">
        <Input v-model="retryHours[i]" inputmode="numeric" class="max-w-32" :aria-label="t('billing.pricing.dunning.delay', { n: i + 1 })" required />
        <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="retryHours.splice(i, 1)"><X class="size-4" /></Button>
      </div>
      <p v-if="retryHours.length === 0 && !dunningStored.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.none") }}</p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="retryHours.length >= 6" @click="retryHours.push('24')"><Plus class="size-4" />{{ t("billing.pricing.dunning.add") }}</Button>
        <Button type="button" :disabled="setDunning.isPending.value" @click="saveDunning">{{ t("billing.pricing.dunning.save") }}</Button>
      </div>
      <p v-if="dunningError" class="text-destructive text-sm" role="alert">{{ dunningError }}</p>
      <p v-if="setDunning.error.value" class="text-destructive text-sm" role="alert">{{ errorText(setDunning.error.value) }}</p>
      <p v-if="setDunning.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.pricing.dunning.saved") }}</p>
    </Card>

    <Card>
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.tax.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.tax.hint") }}</p>
      <p v-if="stored.error.value" class="text-destructive text-sm" role="alert">{{ errorText(stored.error.value) }}</p>
      <datalist id="tax-countries"><option v-for="c in COUNTRY_HINTS" :key="c" :value="c" /></datalist>
      <div v-for="(r, i) in rates" :key="i" class="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-3" role="group" :aria-label="t('billing.pricing.tax.rate', { n: i + 1 })">
        <Input v-model="r.country" list="tax-countries" maxlength="2" :aria-label="t('billing.pricing.tax.country', { n: i + 1 })" :placeholder="t('billing.pricing.tax.anyCountry')" />
        <Input v-model="r.name" :aria-label="t('billing.pricing.tax.name', { n: i + 1 })" :placeholder="t('billing.pricing.tax.namePlaceholder')" required />
        <Input v-model="r.percent" inputmode="decimal" :aria-label="t('billing.pricing.tax.percent', { n: i + 1 })" required />
        <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="rates.splice(i, 1)"><X class="size-4" /></Button>
      </div>
      <p v-if="rates.length === 0 && !stored.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.pricing.tax.none") }}</p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" @click="rates.push({ country: '', name: '', percent: '' })"><Plus class="size-4" />{{ t("billing.pricing.tax.add") }}</Button>
        <Button type="button" :disabled="setRates.isPending.value" @click="saveRates">{{ t("billing.pricing.tax.save") }}</Button>
      </div>
      <p v-if="taxError" class="text-destructive text-sm" role="alert">{{ taxError }}</p>
      <p v-if="setRates.error.value" class="text-destructive text-sm" role="alert">{{ errorText(setRates.error.value) }}</p>
      <p v-if="setRates.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.pricing.tax.saved") }}</p>
    </Card>
  </fieldset>
</template>
