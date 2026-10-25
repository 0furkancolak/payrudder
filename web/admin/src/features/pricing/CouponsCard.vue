<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { parsePercent, useCouponActions, useCoupons, type CouponTarget, type NewCoupon } from "./api";
import PromotionCodesPanel from "./PromotionCodesPanel.vue";
import { targetKey, TARGETS } from "./promotions";

const props = defineProps<{ tenantId: string }>();
const { t, locale } = useI18n();
const errorText = useErrorText();
const tenantId = computed(() => props.tenantId);

const coupons = useCoupons(tenantId);
const actions = useCouponActions(tenantId);
const code = ref("");
const name = ref("");
const kind = ref<"percent" | "amount" | "bonus">("percent");
const value = ref("");
const currency = ref("USD");
const targets = ref<CouponTarget[]>(["COUPON_TARGET_SUBSCRIPTION", "COUPON_TARGET_CREDIT_PURCHASE", "COUPON_TARGET_PAYMENT"]);
const duration = ref<NonNullable<NewCoupon["duration"]>>("COUPON_DURATION_ONCE");
const periods = ref("3");
const max = ref("");
const withoutCode = ref(false);
const couponError = ref("");
const selected = ref("");

// A bonus coupon is for credit purchases only; the others work wherever they are ticked.
const effectiveTargets = computed<CouponTarget[]>(() => (kind.value === "bonus" ? ["COUPON_TARGET_CREDIT_PURCHASE"] : targets.value));
const forSubscription = computed(() => effectiveTargets.value.includes("COUPON_TARGET_SUBSCRIPTION"));

function toggle(v: CouponTarget, on: boolean) {
  targets.value = on ? [...new Set([...targets.value, v])] : targets.value.filter((x) => x !== v);
}

function createCoupon() {
  couponError.value = "";
  if (effectiveTargets.value.length === 0) {
    couponError.value = t("billing.pricing.promotions.noTarget");
    return;
  }
  const body: NewCoupon = { code: code.value.trim(), name: name.value.trim() };
  // Left out when the coupon works everywhere (the default), and for a bonus coupon, which is for credit purchases only.
  if (kind.value !== "bonus" && effectiveTargets.value.length < TARGETS.length) body.applies_to = effectiveTargets.value;
  if (kind.value === "percent" || kind.value === "bonus") {
    const bps = kind.value === "bonus" ? parseBonus(value.value) : parsePercent(value.value);
    if (bps === null || bps === 0) {
      couponError.value = t(kind.value === "bonus" ? "billing.pricing.promotions.badBonus" : "billing.pricing.coupons.badPercent");
      return;
    }
    if (kind.value === "bonus") body.bonus_percent_bps = bps;
    else body.percent_bps = bps;
  } else {
    const minor = parseMajor(value.value, currency.value);
    if (minor === null || minor === "0") {
      couponError.value = t("billing.pricing.coupons.badAmount", { currency: currency.value });
      return;
    }
    body.amount_minor = minor;
    body.currency = currency.value;
  }
  if (forSubscription.value) {
    body.duration = duration.value;
    if (duration.value === "COUPON_DURATION_REPEATING") body.duration_periods = Number(periods.value);
  }
  if (max.value !== "") body.max_redemptions = Number(max.value);
  if (withoutCode.value) body.without_default_code = true;
  actions.create.mutate(body, {
    onSuccess: () => {
      code.value = name.value = value.value = max.value = "";
    },
  });
}

/** Basis points of a typed percentage of extra credits: 0.01 to 1000 with at most two decimals, done on the text. */
function parseBonus(input: string): number | null {
  const m = /^(\d{1,4})(?:[.,](\d{1,2}))?$/.exec(input.trim());
  if (!m) return null;
  const bps = Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
  return bps <= 100000 ? bps : null;
}

const pct = (bps: number) => `${(bps / 100).toLocaleString(locale.value)} %`;
const describe = (c: { percent_bps?: number; amount_minor?: string; currency?: string; bonus_percent_bps?: number }) =>
  c.bonus_percent_bps ? t("billing.pricing.promotions.bonusValue", { pct: pct(c.bonus_percent_bps) }) : c.percent_bps ? pct(c.percent_bps) : formatMinor(c.amount_minor, c.currency, locale.value);
const durationText = (c: { duration?: string; duration_periods?: number; applies_to?: string[] }) => {
  // A coupon with no targets listed works everywhere, a subscription included.
  const at = c.applies_to ?? [];
  if (at.length > 0 && !at.includes("COUPON_TARGET_SUBSCRIPTION")) return "—";
  return c.duration === "COUPON_DURATION_REPEATING"
    ? t("billing.pricing.coupons.repeatingFor", { n: c.duration_periods ?? 0 })
    : c.duration === "COUPON_DURATION_FOREVER"
      ? t("billing.pricing.coupons.forever")
      : t("billing.pricing.coupons.once");
};
const where = (c: { applies_to?: string[] }) => (c.applies_to ?? []).map((x) => t(`billing.pricing.promotions.targets.${targetKey(x)}`)).join(", ");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.pricing.promotions.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="form" :aria-label="t('billing.pricing.coupons.formLabel')" @submit.prevent="createCoupon">
      <Input v-model="code" :aria-label="t('billing.pricing.coupons.code')" :placeholder="t('billing.pricing.coupons.code')" pattern="[A-Za-z0-9_\-]{2,40}" required />
      <Input v-model="name" :aria-label="t('billing.pricing.coupons.name')" :placeholder="t('billing.pricing.coupons.name')" />
      <NativeSelect v-model="kind" :aria-label="t('billing.pricing.coupons.kind')">
        <option value="percent">{{ t("billing.pricing.promotions.kinds.percent") }}</option>
        <option value="amount">{{ t("billing.pricing.promotions.kinds.amount") }}</option>
        <option value="bonus">{{ t("billing.pricing.promotions.kinds.bonus") }}</option>
      </NativeSelect>
      <div class="grid grid-cols-[1fr_auto] gap-2">
        <Input v-model="value" inputmode="decimal" :aria-label="t('billing.pricing.coupons.value')" :placeholder="t(kind === 'bonus' ? 'billing.pricing.promotions.valueBonus' : 'billing.pricing.coupons.value')" required />
        <CurrencySelect v-if="kind === 'amount'" v-model="currency" :aria-label="t('billing.pricing.coupons.currency')" />
      </div>
      <fieldset v-if="kind !== 'bonus'" class="m-0 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 border-0 p-0 sm:col-span-2">
        <legend class="text-muted-foreground mb-1 text-xs">{{ t("billing.pricing.promotions.appliesTo") }}</legend>
        <label v-for="x in TARGETS" :key="x.value" class="flex items-center gap-2 text-sm">
          <input type="checkbox" class="size-4" :checked="targets.includes(x.value)" @change="toggle(x.value, ($event.target as HTMLInputElement).checked)" />
          {{ t(`billing.pricing.promotions.targets.${x.key}`) }}
        </label>
      </fieldset>
      <p v-else class="text-muted-foreground text-xs sm:col-span-2">{{ t("billing.pricing.promotions.bonusNote") }}</p>
      <template v-if="forSubscription">
        <NativeSelect v-model="duration" :aria-label="t('billing.pricing.coupons.duration')">
          <option value="COUPON_DURATION_ONCE">{{ t("billing.pricing.coupons.once") }}</option>
          <option value="COUPON_DURATION_REPEATING">{{ t("billing.pricing.coupons.repeating") }}</option>
          <option value="COUPON_DURATION_FOREVER">{{ t("billing.pricing.coupons.forever") }}</option>
        </NativeSelect>
        <Input v-if="duration === 'COUPON_DURATION_REPEATING'" v-model="periods" type="number" min="1" :aria-label="t('billing.pricing.coupons.periods')" :placeholder="t('billing.pricing.coupons.periods')" />
      </template>
      <Input v-model="max" type="number" min="1" :aria-label="t('billing.pricing.coupons.max')" :placeholder="t('billing.pricing.coupons.max')" />
      <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="withoutCode" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.withoutDefaultCode") }}</label>
      <Button type="submit" :disabled="actions.create.isPending.value">{{ t("billing.pricing.coupons.create") }}</Button>
    </form>
    <p v-if="couponError" class="text-destructive text-sm" role="alert">{{ couponError }}</p>
    <p v-if="actions.create.error.value" class="text-destructive text-sm" role="alert">{{ errorText(actions.create.error.value) }}</p>
    <p v-if="actions.deactivate.error.value" class="text-destructive text-sm" role="alert">{{ errorText(actions.deactivate.error.value) }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
          <TableHead>{{ t("billing.pricing.coupons.value") }}</TableHead>
          <TableHead>{{ t("billing.pricing.promotions.appliesTo") }}</TableHead>
          <TableHead>{{ t("billing.pricing.coupons.duration") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.promotions.codeCount") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.coupons.redeemed") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in coupons.data.value?.coupons ?? []" :key="c.code" :data-state="selected === c.code ? 'selected' : undefined">
          <TableCell class="mono font-medium">{{ c.code }} <span class="text-muted-foreground text-xs">{{ c.name }}</span></TableCell>
          <TableCell class="tabular-nums">{{ describe(c) }}</TableCell>
          <TableCell class="text-xs">{{ where(c) }}</TableCell>
          <TableCell>{{ durationText(c) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ c.promotion_code_count ?? 0 }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ c.redeemed }}<template v-if="c.max_redemptions"> / {{ c.max_redemptions }}</template></TableCell>
          <TableCell><Badge :variant="c.active ? 'success' : 'secondary'">{{ c.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></TableCell>
          <TableCell class="text-end whitespace-nowrap">
            <Button size="sm" variant="outline" class="me-2" :aria-pressed="selected === c.code" @click="selected = selected === c.code ? '' : (c.code ?? '')">
              {{ selected === c.code ? t("billing.pricing.promotions.hideCodes") : t("billing.pricing.promotions.manageCodes") }}
            </Button>
            <Button v-if="c.active" size="sm" variant="ghost" @click="actions.deactivate.mutate(c.code ?? '')">{{ t("billing.pricing.coupons.deactivate") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!coupons.isPending.value && (coupons.data.value?.coupons ?? []).length === 0 && !coupons.error.value" class="text-muted-foreground text-center text-sm">
      {{ t("billing.pricing.coupons.empty") }}
    </p>
    <PromotionCodesPanel v-if="selected" :key="selected" :tenant-id="tenantId" :coupon="selected" />
  </Card>
</template>
