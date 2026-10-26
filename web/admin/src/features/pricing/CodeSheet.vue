<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCreditPacks } from "@/features/credits/api";
import { useTenantCurrency } from "@/features/tenants/currency";
import { Button } from "@/shared/ui/button";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { NativeSelect } from "@/shared/ui/native-select";
import { SegmentedControl } from "@/shared/ui/segmented";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/shared/ui/sheet";
import { Textarea } from "@/shared/ui/textarea";
import { useCodeActions, usePlanCodes } from "./api";
import { badCodes, codesCsv, codeRules, describeCoupon, emptyCodeForm, splitCodes, toTemplate } from "./promotions";

type Coupon = { code?: string; name?: string; percent_bps?: number; amount_minor?: string; currency?: string; bonus_percent_bps?: number };

/**
 * New promotion codes in a sheet: for one coupon (named, or generated at random), with the limit, expiry and rules every code of the request shares. When
 * the codes are made the sheet shows them, to copy or download, because the generated ones are not shown anywhere else as a list.
 */
const props = defineProps<{ tenantId: string; coupons: readonly Coupon[]; preset?: string; fixed?: boolean }>();
const open = defineModel<boolean>("open", { required: true });
const { t, locale } = useI18n();

const tenantCurrency = useTenantCurrency(() => props.tenantId);
const act = useCodeActions(() => props.tenantId);
const plans = usePlanCodes(() => props.tenantId);
const packs = useCreditPacks(() => props.tenantId);

const coupon = ref("");
const mode = ref<"named" | "random">("named");
const names = ref("");
const count = ref("10");
const prefix = ref("");
const form = ref(emptyCodeForm("USD"));
const chosenPlans = ref<string[]>([]);
const chosenPacks = ref<string[]>([]);
const error = ref("");
const made = ref<string[]>([]);
const copied = ref(false);

// A sheet that was closed opens clean.
watch(open, (v) => {
  if (!v) return;
  coupon.value = props.coupons.some((c) => c.code === props.preset) ? (props.preset ?? "") : (props.coupons[0]?.code ?? "");
  mode.value = "named";
  names.value = "";
  count.value = "10";
  prefix.value = "";
  form.value = emptyCodeForm(tenantCurrency.value || "USD");
  chosenPlans.value = [];
  chosenPacks.value = [];
  error.value = "";
  made.value = [];
  copied.value = false;
  act.create.reset();
});

const chosen = computed(() => props.coupons.find((c) => c.code === coupon.value));
const modes = computed(() => [
  { value: "named", label: t("billing.pricing.promotions.codes.modeNamed") },
  { value: "random", label: t("billing.pricing.promotions.codes.modeRandom") },
]);
const toggle = (list: string[], v: string, on: boolean) => (on ? [...new Set([...list, v])] : list.filter((x) => x !== v));
const packList = computed(() => (packs.data.value?.packs ?? []).filter((p) => p.active !== false));

const summary = computed(() => {
  const out: string[] = [];
  const n = mode.value === "named" ? splitCodes(names.value).length : Number(count.value) || 0;
  out.push(t("billing.coupons.codeSheet.makes", { n, coupon: coupon.value || "…", value: chosen.value ? describeCoupon(chosen.value, t, locale.value) : "…" }));
  const rules = codeRules(
    {
      first_purchase_only: form.value.firstPurchase,
      one_per_customer: form.value.onePerCustomer,
      allowed_plans: chosenPlans.value,
      allowed_packs: chosenPacks.value,
      allowed_currencies: form.value.currencies.trim() ? form.value.currencies.split(/[,\s]+/).filter(Boolean) : [],
      restore_on_reversal: form.value.restore,
    },
    t,
    locale.value,
  );
  if (form.value.minAmount.trim()) rules.push(t("billing.coupons.codeSheet.minimum", { amount: `${form.value.minAmount.trim()} ${form.value.minCurrency}` }));
  out.push(form.value.max.trim() ? t("billing.coupons.codeSheet.usesEach", { n: form.value.max.trim() }) : t("billing.coupons.codeSheet.usesUnlimited"));
  out.push(form.value.expires ? t("billing.coupons.preview.expires", { date: form.value.expires }) : t("billing.coupons.preview.noExpiry"));
  if (rules.length) out.push(rules.join(" · "));
  return out;
});

function submit() {
  error.value = "";
  if (!coupon.value) {
    error.value = t("billing.coupons.errors.coupon");
    return;
  }
  const tpl = toTemplate({ ...form.value, plans: chosenPlans.value.join(","), packs: chosenPacks.value.join(",") });
  if ("error" in tpl) {
    error.value = t(`billing.pricing.promotions.codes.errors.${tpl.error}`);
    return;
  }
  const body: Parameters<typeof act.create.mutate>[0] = { coupon: coupon.value, template: tpl.template };
  if (mode.value === "named") {
    const list = splitCodes(names.value);
    if (list.length === 0 || list.length > 1000 || badCodes(list).length > 0) {
      error.value = t("billing.pricing.promotions.codes.errors.names");
      return;
    }
    body.codes = list;
  } else {
    const n = Number(count.value);
    if (!Number.isInteger(n) || n < 1 || n > 1000 || !/^[A-Za-z0-9_-]{0,20}$/.test(prefix.value)) {
      error.value = t("billing.pricing.promotions.codes.errors.count");
      return;
    }
    body.count = n;
    if (prefix.value) body.prefix = prefix.value;
  }
  act.create.mutate(body, { onSuccess: (r) => (made.value = (r.codes ?? []).map((c) => c.code ?? "").filter(Boolean)) });
}

async function copyAll() {
  try {
    await navigator.clipboard.writeText(made.value.join("\n"));
    copied.value = true;
  } catch {
    /* the list is selectable */
  }
}
function download() {
  const url = URL.createObjectURL(new Blob([codesCsv(made.value)], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `promotion-codes-${coupon.value}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent>
      <SheetHeader>
        <SheetTitle>{{ t("billing.pricing.promotions.codes.create") }}</SheetTitle>
        <SheetDescription>{{ t("billing.pricing.promotions.codes.hint") }}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <div v-if="made.length" class="grid gap-3" role="status" data-testid="codes-made">
          <p class="text-sm font-medium">{{ t("billing.pricing.promotions.codes.created", { n: made.length }) }}</p>
          <Textarea :model-value="made.join('\n')" readonly rows="8" class="mono" :aria-label="t('billing.pricing.promotions.codes.createdList')" data-testid="made-codes" @focus="($event.target as HTMLTextAreaElement).select()" />
          <div class="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" @click="copyAll">{{ copied ? t("billing.pricing.promotions.codes.copied") : t("billing.pricing.promotions.codes.copyAll") }}</Button>
            <Button type="button" variant="outline" size="sm" @click="download">{{ t("billing.pricing.promotions.codes.download") }}</Button>
          </div>
        </div>
        <form v-else id="codes-form" class="grid gap-5" role="form" :aria-label="t('billing.pricing.promotions.codes.formLabel')" @submit.prevent="submit">
          <div class="grid gap-1.5">
            <Label for="code-coupon">{{ t("billing.pricing.promotions.redemptions.coupon") }}</Label>
            <NativeSelect id="code-coupon" v-model="coupon" :disabled="fixed">
              <option v-for="c in coupons" :key="c.code" :value="c.code">{{ c.code }}{{ c.name ? ` · ${c.name}` : "" }} · {{ describeCoupon(c, t, locale) }}</option>
            </NativeSelect>
          </div>

          <div class="grid gap-2">
            <span class="text-sm font-medium">{{ t("billing.pricing.promotions.codes.mode") }}</span>
            <SegmentedControl v-model="mode" :options="modes" :label="t('billing.pricing.promotions.codes.mode')" />
          </div>
          <div v-if="mode === 'named'" class="grid gap-1.5">
            <Label for="code-names">{{ t("billing.pricing.promotions.codes.names") }}</Label>
            <Textarea id="code-names" v-model="names" rows="3" class="mono" :placeholder="t('billing.pricing.promotions.codes.namesPlaceholder')" />
            <p class="text-muted-foreground text-xs">{{ t("billing.coupons.codeSheet.namesHint") }}</p>
          </div>
          <div v-else class="grid gap-4 sm:grid-cols-2">
            <div class="grid content-start gap-1.5">
              <Label for="code-count">{{ t("billing.pricing.promotions.codes.count") }}</Label>
              <Input id="code-count" v-model="count" inputmode="numeric" />
            </div>
            <div class="grid content-start gap-1.5">
              <Label for="code-prefix">{{ t("billing.pricing.promotions.codes.prefix") }}</Label>
              <Input id="code-prefix" v-model="prefix" maxlength="20" class="mono" placeholder="SPR-" />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <div class="grid content-start gap-1.5">
              <Label for="code-max">{{ t("billing.pricing.promotions.codes.maxPerCode") }}</Label>
              <Input id="code-max" v-model="form.max" inputmode="numeric" />
            </div>
            <div class="grid content-start gap-1.5">
              <Label for="code-expires">{{ t("billing.pricing.promotions.codes.expires") }}</Label>
              <Input id="code-expires" v-model="form.expires" type="date" />
            </div>
          </div>

          <div class="grid gap-1.5">
            <Label for="code-min">{{ t("billing.pricing.promotions.codes.minAmount") }}</Label>
            <div class="grid grid-cols-[1fr_auto] gap-2">
              <Input id="code-min" v-model="form.minAmount" inputmode="decimal" />
              <CurrencySelect v-model="form.minCurrency" :aria-label="t('billing.pricing.promotions.codes.minCurrency')" />
            </div>
          </div>
          <div class="grid gap-1.5">
            <Label for="code-currencies">{{ t("billing.pricing.promotions.codes.allowedCurrencies") }}</Label>
            <Input id="code-currencies" v-model="form.currencies" class="mono" placeholder="USD, EUR" />
          </div>

          <fieldset class="m-0 grid min-w-0 gap-2 border-0 p-0">
            <legend class="mb-1 text-sm font-medium">{{ t("billing.coupons.codeSheet.plans") }}</legend>
            <p v-if="(plans.data.value ?? []).length === 0" class="text-muted-foreground text-xs">{{ t("billing.coupons.codeSheet.noPlans") }}</p>
            <p v-else class="text-muted-foreground text-xs">{{ t("billing.coupons.codeSheet.plansHint") }}</p>
            <label v-for="p in plans.data.value ?? []" :key="p.code" class="flex items-center gap-2 text-sm">
              <input type="checkbox" class="size-4" :checked="chosenPlans.includes(p.code ?? '')" @change="chosenPlans = toggle(chosenPlans, p.code ?? '', ($event.target as HTMLInputElement).checked)" />
              <span class="mono text-xs">{{ p.code }}</span><span class="text-muted-foreground">{{ p.name }}</span>
            </label>
          </fieldset>
          <fieldset v-if="packList.length > 0" class="m-0 grid min-w-0 gap-2 border-0 p-0">
            <legend class="mb-1 text-sm font-medium">{{ t("billing.coupons.codeSheet.packs") }}</legend>
            <label v-for="p in packList" :key="p.id" class="flex items-center gap-2 text-sm">
              <input type="checkbox" class="size-4" :checked="chosenPacks.includes(p.id ?? '')" @change="chosenPacks = toggle(chosenPacks, p.id ?? '', ($event.target as HTMLInputElement).checked)" />
              {{ p.name }}
            </label>
          </fieldset>

          <div class="grid gap-2">
            <label class="flex items-center gap-2 text-sm"><input v-model="form.firstPurchase" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.firstPurchase") }}</label>
            <label class="flex items-center gap-2 text-sm"><input v-model="form.onePerCustomer" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.onePerCustomer") }}</label>
            <label class="flex items-center gap-2 text-sm"><input v-model="form.restore" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.restore") }}</label>
          </div>

          <section class="bg-muted/60 grid gap-2 rounded-2xl p-4 text-sm" :aria-label="t('billing.coupons.preview.title')" data-testid="code-preview">
            <h3 class="text-muted-foreground text-xs font-medium tracking-wide uppercase">{{ t("billing.coupons.preview.title") }}</h3>
            <p class="font-medium">{{ summary[0] }}</p>
            <ul class="text-muted-foreground grid gap-1 text-xs">
              <li v-for="l in summary.slice(1)" :key="l">{{ l }}</li>
            </ul>
          </section>

          <ErrorText :message="error" />
          <ErrorText :error="act.create.error.value" />
        </form>
      </SheetBody>
      <SheetFooter>
        <template v-if="made.length">
          <Button type="button" @click="open = false">{{ t("billing.coupons.codeSheet.done") }}</Button>
        </template>
        <template v-else>
          <Button variant="ghost" type="button" @click="open = false">{{ t("common.cancel") }}</Button>
          <Button type="submit" form="codes-form" :disabled="act.create.isPending.value || coupons.length === 0">{{ t("billing.pricing.promotions.codes.create") }}</Button>
        </template>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
