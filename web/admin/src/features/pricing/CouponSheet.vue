<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useTenantCurrency } from "@/features/tenants/currency";
import { formatMinor } from "@/shared/lib/money";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { SegmentedControl } from "@/shared/ui/segmented";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { useCouponActions, type CouponTarget } from "./api";
import { couponEffect, describeDuration, effectiveTargets, emptyCouponForm, forSubscription, TARGETS, toCouponBody } from "./promotions";

/**
 * A new coupon in a sheet: what it takes off (a percentage, an amount in a currency, or extra credits), where it works, how long a subscription keeps it,
 * and its limit and expiry. A line under the form says in plain words what the coupon will do with the values as they stand.
 */
const props = defineProps<{ tenantId: string }>();
const open = defineModel<boolean>("open", { required: true });
const emit = defineEmits<{ created: [code: string] }>();
const { t, locale } = useI18n();

const tenantCurrency = useTenantCurrency(() => props.tenantId);
const actions = useCouponActions(() => props.tenantId);
const form = ref(emptyCouponForm("USD"));
const error = ref("");

// A sheet that was closed opens clean: the last values and errors are not shown again.
watch(open, (v) => {
  if (!v) return;
  form.value = emptyCouponForm(tenantCurrency.value || "USD");
  error.value = "";
  actions.create.reset();
});

const kinds = computed(() => (["percent", "amount", "bonus"] as const).map((k) => ({ value: k, label: t(`billing.pricing.promotions.kinds.${k}`) })));
const durations = computed(() => [
  { value: "COUPON_DURATION_ONCE", label: t("billing.pricing.coupons.once") },
  { value: "COUPON_DURATION_REPEATING", label: t("billing.pricing.coupons.repeating") },
  { value: "COUPON_DURATION_FOREVER", label: t("billing.pricing.coupons.forever") },
]);
const valueLabel = computed(() => t(`billing.coupons.sheet.value.${form.value.kind}`));
const targets = computed(() => effectiveTargets(form.value));
const onSubscription = computed(() => forSubscription(form.value));

function toggle(v: CouponTarget, on: boolean) {
  form.value.targets = on ? [...new Set([...form.value.targets, v])] : form.value.targets.filter((x) => x !== v);
}

const effect = computed(() => couponEffect(form.value.kind, form.value.value, form.value.currency));
const effectText = computed(() => {
  const e = effect.value;
  if (!e) return t("billing.coupons.preview.noValue");
  const pct = (bps: number) => `${(bps / 100).toLocaleString(locale.value)} %`;
  if (e.kind === "percent") return t("billing.coupons.preview.percent", { pct: pct(e.bps), before: formatMinor(e.before, e.currency, locale.value), after: formatMinor(e.after, e.currency, locale.value) });
  if (e.kind === "amount") return t("billing.coupons.preview.amount", { off: formatMinor(e.off, e.currency, locale.value), before: formatMinor(e.before, e.currency, locale.value), after: formatMinor(e.after, e.currency, locale.value) });
  return t("billing.coupons.preview.bonus", { pct: pct(e.bps), credits: e.credits.toLocaleString(locale.value), total: (e.credits + e.bonus).toLocaleString(locale.value) });
});
const lines = computed(() => {
  const out: string[] = [];
  if (targets.value.length > 0) out.push(t("billing.coupons.preview.where", { list: targets.value.map((x) => t(`billing.pricing.promotions.targets.${TARGETS.find((y) => y.value === x)?.key}`)).join(", ") }));
  if (onSubscription.value) out.push(t("billing.coupons.preview.duration", { duration: describeDuration({ duration: form.value.duration, duration_periods: Number(form.value.periods) || 0 }, t).toLowerCase() }));
  out.push(String(form.value.max).trim() !== "" ? t("billing.coupons.preview.max", { n: String(form.value.max).trim() }) : t("billing.coupons.preview.noMax"));
  out.push(form.value.expires !== "" ? t("billing.coupons.preview.expires", { date: form.value.expires }) : t("billing.coupons.preview.noExpiry"));
  if (form.value.defaultCode && form.value.code.trim() !== "") out.push(t("billing.coupons.preview.code", { code: form.value.code.trim() }));
  return out;
});

function submit() {
  error.value = "";
  const r = toCouponBody(form.value);
  if ("error" in r) {
    error.value = t(`billing.coupons.errors.${r.error}`, { currency: form.value.currency });
    return;
  }
  actions.create.mutate(r.body, {
    onSuccess: () => {
      emit("created", r.body.code);
      open.value = false;
    },
  });
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.coupons.sheet.title") }}</SheetTitle>
        <SheetDescription>{{ t("billing.coupons.sheet.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <form id="coupon-form" class="grid gap-5" role="form" :aria-label="t('billing.pricing.coupons.formLabel')" @submit.prevent="submit">
          <div class="grid gap-1.5">
            <Label for="coupon-code">{{ t("billing.pricing.coupons.code") }}</Label>
            <Input id="coupon-code" v-model="form.code" class="mono" autocomplete="off" placeholder="WELCOME10" pattern="[A-Za-z0-9_\-]{2,40}" required />
            <p class="text-muted-foreground text-xs">{{ t("billing.coupons.sheet.codeHint") }}</p>
          </div>
          <div class="grid gap-1.5">
            <Label for="coupon-name">{{ t("billing.pricing.coupons.name") }}</Label>
            <Input id="coupon-name" v-model="form.name" :placeholder="t('billing.coupons.sheet.namePlaceholder')" />
          </div>

          <div class="grid gap-2">
            <span class="text-sm font-medium">{{ t("billing.pricing.coupons.kind") }}</span>
            <SegmentedControl v-model="form.kind" :options="kinds" :label="t('billing.pricing.coupons.kind')" />
          </div>
          <div class="grid gap-1.5">
            <Label for="coupon-value">{{ valueLabel }}</Label>
            <div class="grid grid-cols-[1fr_auto] gap-2">
              <Input id="coupon-value" v-model="form.value" inputmode="decimal" :placeholder="form.kind === 'amount' ? '4.99' : '10'" required />
              <CurrencySelect v-if="form.kind === 'amount'" v-model="form.currency" :aria-label="t('billing.pricing.coupons.currency')" />
              <span v-else class="text-muted-foreground self-center text-sm">%</span>
            </div>
          </div>

          <fieldset v-if="form.kind !== 'bonus'" class="m-0 grid min-w-0 gap-2 border-0 p-0">
            <legend class="mb-1 text-sm font-medium">{{ t("billing.pricing.promotions.appliesTo") }}</legend>
            <label v-for="x in TARGETS" :key="x.value" class="flex items-center gap-2 text-sm">
              <input type="checkbox" class="size-4" :checked="form.targets.includes(x.value)" @change="toggle(x.value, ($event.target as HTMLInputElement).checked)" />
              {{ t(`billing.pricing.promotions.targets.${x.key}`) }}
            </label>
          </fieldset>
          <p v-else class="text-muted-foreground text-xs">{{ t("billing.pricing.promotions.bonusNote") }}</p>

          <template v-if="onSubscription">
            <div class="grid gap-2">
              <span class="text-sm font-medium">{{ t("billing.pricing.coupons.duration") }}</span>
              <SegmentedControl v-model="form.duration" :options="durations" :label="t('billing.pricing.coupons.duration')" />
              <p class="text-muted-foreground text-xs">{{ t("billing.coupons.sheet.durationHint") }}</p>
            </div>
            <div v-if="form.duration === 'COUPON_DURATION_REPEATING'" class="grid gap-1.5">
              <Label for="coupon-periods">{{ t("billing.pricing.coupons.periods") }}</Label>
              <Input id="coupon-periods" v-model="form.periods" inputmode="numeric" class="max-w-28" />
            </div>
          </template>

          <div class="grid gap-4 sm:grid-cols-2">
            <div class="grid content-start gap-1.5">
              <Label for="coupon-max">{{ t("billing.pricing.coupons.max") }}</Label>
              <Input id="coupon-max" v-model="form.max" inputmode="numeric" :placeholder="t('billing.coupons.sheet.noLimit')" />
            </div>
            <div class="grid content-start gap-1.5">
              <Label for="coupon-expires">{{ t("billing.coupons.sheet.expires") }}</Label>
              <Input id="coupon-expires" v-model="form.expires" type="date" />
            </div>
          </div>
          <label class="flex items-start gap-2 text-sm">
            <input v-model="form.defaultCode" type="checkbox" class="mt-0.5 size-4" />
            <span>{{ t("billing.coupons.sheet.defaultCode") }}<span class="text-muted-foreground block text-xs">{{ t("billing.coupons.sheet.defaultCodeHint") }}</span></span>
          </label>

          <section class="bg-muted/60 grid gap-2 rounded-2xl p-4 text-sm" :aria-label="t('billing.coupons.preview.title')" data-testid="coupon-preview">
            <h3 class="text-muted-foreground text-xs font-medium tracking-wide uppercase">{{ t("billing.coupons.preview.title") }}</h3>
            <p class="font-medium" data-testid="coupon-effect">{{ effectText }}</p>
            <ul class="text-muted-foreground grid gap-1 text-xs">
              <li v-for="l in lines" :key="l">{{ l }}</li>
            </ul>
          </section>

          <ErrorText :message="error" />
          <ErrorText :error="actions.create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
        <Button type="submit" form="coupon-form" :disabled="actions.create.isPending.value">{{ t("billing.pricing.coupons.create") }}</Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
