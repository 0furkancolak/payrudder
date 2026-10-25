<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { Plus, X } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { FALLBACK_CURRENCY, COUNTRY_HINTS } from "@/shared/lib/currencies";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { useCanWrite } from "@/shared/lib/access";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { parsePercent, useCouponActions, useCoupons, useDunningSettings, useSetDunningSettings, useSetTaxRates, useTaxRates, type NewCoupon, type Rate } from "./api";
import { CurrencySelect } from "@/shared/ui/search-select";

const { t, locale } = useI18n();
const canWrite = useCanWrite();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));

// --- coupons
const coupons = useCoupons(tenantId);
const actions = useCouponActions(tenantId);
const code = ref("");
const name = ref("");
const kind = ref<"percent" | "amount">("percent");
const value = ref("");
const currency = ref<string>(FALLBACK_CURRENCY);
const duration = ref<NewCoupon["duration"]>("COUPON_DURATION_ONCE");
const periods = ref("3");
const max = ref("");
const couponError = ref("");

function createCoupon() {
  couponError.value = "";
  const body: NewCoupon = { code: code.value.trim(), name: name.value.trim(), duration: duration.value };
  if (kind.value === "percent") {
    const bps = parsePercent(value.value);
    if (bps === null || bps === 0) {
      couponError.value = t("billing.pricing.coupons.badPercent");
      return;
    }
    body.percent_bps = bps;
  } else {
    const minor = parseMajor(value.value, currency.value);
    if (minor === null || minor === "0") {
      couponError.value = t("billing.pricing.coupons.badAmount", { currency: currency.value });
      return;
    }
    body.amount_minor = minor;
    body.currency = currency.value;
  }
  if (duration.value === "COUPON_DURATION_REPEATING") body.duration_periods = Number(periods.value);
  if (max.value !== "") body.max_redemptions = Number(max.value);
  actions.create.mutate(body, {
    onSuccess: () => {
      code.value = name.value = value.value = max.value = "";
    },
  });
}

const describe = (c: { percent_bps?: number; amount_minor?: string; currency?: string }) =>
  c.percent_bps ? `${(c.percent_bps / 100).toLocaleString(locale.value)} %` : formatMinor(c.amount_minor, c.currency, locale.value);
const durationText = (c: { duration?: string; duration_periods?: number }) =>
  c.duration === "COUPON_DURATION_REPEATING"
    ? t("billing.pricing.coupons.repeatingFor", { n: c.duration_periods ?? 0 })
    : c.duration === "COUPON_DURATION_FOREVER"
      ? t("billing.pricing.coupons.forever")
      : t("billing.pricing.coupons.once");

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
  <!-- A viewer reads the coupons, the retry schedule and the tax rates; the controls that change them are switched off (the server refuses them anyway). -->
  <fieldset :disabled="!canWrite" class="m-0 min-w-0 border-0 p-0">
    <Card class="mb-6">
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.coupons.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.coupons.hint") }}</p>
      <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="form" :aria-label="t('billing.pricing.coupons.formLabel')" @submit.prevent="createCoupon">
        <Input v-model="code" :aria-label="t('billing.pricing.coupons.code')" :placeholder="t('billing.pricing.coupons.code')" pattern="[A-Za-z0-9_\-]{2,40}" required />
        <Input v-model="name" :aria-label="t('billing.pricing.coupons.name')" :placeholder="t('billing.pricing.coupons.name')" />
        <NativeSelect v-model="kind" :aria-label="t('billing.pricing.coupons.kind')">
          <option value="percent">{{ t("billing.pricing.coupons.percent") }}</option>
          <option value="amount">{{ t("billing.pricing.coupons.amount") }}</option>
        </NativeSelect>
        <div class="grid grid-cols-[1fr_auto] gap-2">
          <Input v-model="value" inputmode="decimal" :aria-label="t('billing.pricing.coupons.value')" :placeholder="t('billing.pricing.coupons.value')" required />
          <CurrencySelect v-if="kind === 'amount'" v-model="currency" :aria-label="t('billing.pricing.coupons.currency')" />
        </div>
        <NativeSelect v-model="duration" :aria-label="t('billing.pricing.coupons.duration')">
          <option value="COUPON_DURATION_ONCE">{{ t("billing.pricing.coupons.once") }}</option>
          <option value="COUPON_DURATION_REPEATING">{{ t("billing.pricing.coupons.repeating") }}</option>
          <option value="COUPON_DURATION_FOREVER">{{ t("billing.pricing.coupons.forever") }}</option>
        </NativeSelect>
        <Input v-if="duration === 'COUPON_DURATION_REPEATING'" v-model="periods" type="number" min="1" :aria-label="t('billing.pricing.coupons.periods')" :placeholder="t('billing.pricing.coupons.periods')" />
        <Input v-model="max" type="number" min="1" :aria-label="t('billing.pricing.coupons.max')" :placeholder="t('billing.pricing.coupons.max')" />
        <Button type="submit" :disabled="actions.create.isPending.value">{{ t("billing.pricing.coupons.create") }}</Button>
      </form>
      <ErrorText :message="couponError" />
      <ErrorText :error="actions.create.error.value" />
      <ErrorText :error="actions.deactivate.error.value" />
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
            <TableHead>{{ t("billing.pricing.coupons.value") }}</TableHead>
            <TableHead>{{ t("billing.pricing.coupons.duration") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.pricing.coupons.redeemed") }}</TableHead>
            <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="c in coupons.data.value?.coupons ?? []" :key="c.code">
            <TableCell class="mono font-medium">{{ c.code }} <span class="text-muted-foreground text-xs">{{ c.name }}</span></TableCell>
            <TableCell class="tabular-nums">{{ describe(c) }}</TableCell>
            <TableCell>{{ durationText(c) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ c.redeemed }}<template v-if="c.max_redemptions"> / {{ c.max_redemptions }}</template></TableCell>
            <TableCell><Badge :variant="c.active ? 'success' : 'secondary'">{{ c.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></TableCell>
            <TableCell class="text-end">
              <Button v-if="c.active" size="sm" variant="ghost" @click="actions.deactivate.mutate(c.code ?? '')">{{ t("billing.pricing.coupons.deactivate") }}</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="!coupons.isPending.value && (coupons.data.value?.coupons ?? []).length === 0 && !coupons.error.value" class="text-muted-foreground text-center text-sm">
        {{ t("billing.pricing.coupons.empty") }}
      </p>
    </Card>

    <Card class="mb-6">
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.dunning.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.hint") }}</p>
      <p v-if="dunningStored.data.value?.defaults_in_use" class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.defaults") }}</p>
      <ErrorText :error="dunningStored.error.value" />
      <div v-for="(_, i) in retryHours" :key="i" class="flex items-center gap-3" role="group" :aria-label="t('billing.pricing.dunning.retry', { n: i + 1 })">
        <Input v-model="retryHours[i]" inputmode="numeric" class="max-w-32" :aria-label="t('billing.pricing.dunning.delay', { n: i + 1 })" required />
        <Button type="button" variant="ghost" size="icon" :aria-label="t('billing.common.remove')" @click="retryHours.splice(i, 1)"><X class="size-4" /></Button>
      </div>
      <p v-if="retryHours.length === 0 && !dunningStored.isPending.value" class="text-muted-foreground text-sm">{{ t("billing.pricing.dunning.none") }}</p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="retryHours.length >= 6" @click="retryHours.push('24')"><Plus class="size-4" />{{ t("billing.pricing.dunning.add") }}</Button>
        <Button type="button" :disabled="setDunning.isPending.value" @click="saveDunning">{{ t("billing.pricing.dunning.save") }}</Button>
      </div>
      <ErrorText :message="dunningError" />
      <ErrorText :error="setDunning.error.value" />
      <p v-if="setDunning.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.pricing.dunning.saved") }}</p>
    </Card>

    <Card>
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.tax.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.tax.hint") }}</p>
      <ErrorText :error="stored.error.value" />
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
      <ErrorText :message="taxError" />
      <ErrorText :error="setRates.error.value" />
      <p v-if="setRates.isSuccess.value" class="text-success text-sm" role="status">{{ t("billing.pricing.tax.saved") }}</p>
    </Card>
  </fieldset>
</template>
