<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { useTenantCurrency } from "@/features/tenants/currency";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Textarea } from "@/shared/ui/textarea";
import { useCodeActions, usePromotionCodes } from "./api";
import { badCodes, codesCsv, emptyCodeForm, splitCodes, toTemplate } from "./promotions";

const props = defineProps<{ tenantId: string; coupon: string }>();
const { t, locale } = useI18n();
const errorText = useErrorText();
const tenantId = computed(() => props.tenantId);
const coupon = computed(() => props.coupon);
const tenantCurrency = useTenantCurrency(tenantId);

// --- the codes already made
const state = ref("");
const search = ref("");
const pageToken = ref("");
const history = ref<string[]>([]);
const codes = usePromotionCodes(tenantId, coupon, state, search, pageToken);
const act = useCodeActions(tenantId);
const rows = computed(() => codes.data.value?.codes ?? []);
function filter() {
  pageToken.value = "";
  history.value = [];
}
function next() {
  const token = codes.data.value?.next_page_token;
  if (!token) return;
  history.value.push(pageToken.value);
  pageToken.value = token;
}
function back() {
  pageToken.value = history.value.pop() ?? "";
}

// --- new codes
const mode = ref<"named" | "random">("named");
const names = ref("");
const count = ref("10");
const prefix = ref("");
const form = ref(emptyCodeForm("USD"));
const seeded = ref(false);
const formError = ref("");
const made = ref<string[]>([]);
const copied = ref(false);

function seedCurrency() {
  if (!seeded.value && tenantCurrency.value) {
    seeded.value = true;
    form.value.minCurrency = tenantCurrency.value;
  }
}

function create() {
  seedCurrency();
  formError.value = "";
  copied.value = false;
  const tpl = toTemplate(form.value);
  if ("error" in tpl) {
    formError.value = t(`billing.pricing.promotions.codes.errors.${tpl.error}`);
    return;
  }
  const body: Parameters<typeof act.create.mutate>[0] = { coupon: props.coupon, template: tpl.template };
  if (mode.value === "named") {
    const list = splitCodes(names.value);
    if (list.length === 0 || list.length > 1000 || badCodes(list).length > 0) {
      formError.value = t("billing.pricing.promotions.codes.errors.names");
      return;
    }
    body.codes = list;
  } else {
    const n = Number(count.value);
    if (!Number.isInteger(n) || n < 1 || n > 1000 || !/^[A-Za-z0-9_-]{0,20}$/.test(prefix.value)) {
      formError.value = t("billing.pricing.promotions.codes.errors.count");
      return;
    }
    body.count = n;
    if (prefix.value) body.prefix = prefix.value;
  }
  act.create.mutate(body, {
    onSuccess: (r) => {
      made.value = (r.codes ?? []).map((c) => c.code ?? "").filter(Boolean);
      names.value = "";
    },
  });
}

async function copyAll() {
  try {
    await navigator.clipboard.writeText(made.value.join("\n"));
    copied.value = true;
  } catch {
    /* the list below is selectable */
  }
}
function download() {
  const url = URL.createObjectURL(new Blob([codesCsv(made.value)], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `promotion-codes-${props.coupon}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

const when = (v?: string | null) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "medium" }).format(new Date(v)) : "—");
type Row = {
  first_purchase_only?: boolean;
  one_per_customer?: boolean;
  min_amount_minor?: string;
  min_amount_currency?: string;
  allowed_plans?: string[];
  allowed_packs?: string[];
  allowed_currencies?: string[];
  restore_on_reversal?: boolean;
};
const rules = (c: Row) => {
  const out: string[] = [];
  if (c.first_purchase_only) out.push(t("billing.pricing.promotions.codes.rule.firstPurchase"));
  if (c.one_per_customer) out.push(t("billing.pricing.promotions.codes.rule.onePerCustomer"));
  if (c.min_amount_minor) out.push(t("billing.pricing.promotions.codes.rule.min", { amount: formatMinor(c.min_amount_minor, c.min_amount_currency, locale.value) }));
  if (c.allowed_currencies?.length) out.push(c.allowed_currencies.join(" "));
  if (c.allowed_plans?.length) out.push(t("billing.pricing.promotions.codes.rule.plans", { list: c.allowed_plans.join(", ") }));
  if (c.allowed_packs?.length) out.push(t("billing.pricing.promotions.codes.rule.packs", { n: c.allowed_packs.length }));
  if (c.restore_on_reversal) out.push(t("billing.pricing.promotions.codes.rule.restore"));
  return out;
};
</script>

<template>
  <section class="border-border mt-2 grid gap-4 rounded-md border p-4" :aria-label="t('billing.pricing.promotions.codes.title', { coupon })">
    <h3 class="text-base font-semibold">{{ t("billing.pricing.promotions.codes.title", { coupon }) }}</h3>
    <p class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.codes.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" role="form" :aria-label="t('billing.pricing.promotions.codes.formLabel')" @submit.prevent="create">
      <NativeSelect v-model="mode" :aria-label="t('billing.pricing.promotions.codes.mode')" @focus="seedCurrency">
        <option value="named">{{ t("billing.pricing.promotions.codes.modeNamed") }}</option>
        <option value="random">{{ t("billing.pricing.promotions.codes.modeRandom") }}</option>
      </NativeSelect>
      <Textarea v-if="mode === 'named'" v-model="names" rows="3" class="sm:col-span-2" :aria-label="t('billing.pricing.promotions.codes.names')" :placeholder="t('billing.pricing.promotions.codes.namesPlaceholder')" />
      <template v-else>
        <Input v-model="count" inputmode="numeric" :aria-label="t('billing.pricing.promotions.codes.count')" :placeholder="t('billing.pricing.promotions.codes.count')" />
        <Input v-model="prefix" maxlength="20" :aria-label="t('billing.pricing.promotions.codes.prefix')" :placeholder="t('billing.pricing.promotions.codes.prefix')" />
      </template>
      <Input v-model="form.max" inputmode="numeric" :aria-label="t('billing.pricing.promotions.codes.maxPerCode')" :placeholder="t('billing.pricing.promotions.codes.maxPerCode')" />
      <Input v-model="form.expires" type="date" :aria-label="t('billing.pricing.promotions.codes.expires')" :title="t('billing.pricing.promotions.codes.expires')" />
      <div class="grid grid-cols-[1fr_auto] gap-2">
        <Input v-model="form.minAmount" inputmode="decimal" :aria-label="t('billing.pricing.promotions.codes.minAmount')" :placeholder="t('billing.pricing.promotions.codes.minAmount')" />
        <CurrencySelect v-model="form.minCurrency" :aria-label="t('billing.pricing.promotions.codes.minCurrency')" />
      </div>
      <Input v-model="form.currencies" :aria-label="t('billing.pricing.promotions.codes.allowedCurrencies')" :placeholder="t('billing.pricing.promotions.codes.allowedCurrencies')" />
      <Input v-model="form.plans" :aria-label="t('billing.pricing.promotions.codes.allowedPlans')" :placeholder="t('billing.pricing.promotions.codes.allowedPlans')" />
      <Input v-model="form.packs" :aria-label="t('billing.pricing.promotions.codes.allowedPacks')" :placeholder="t('billing.pricing.promotions.codes.allowedPacks')" />
      <div class="grid gap-1 sm:col-span-2 lg:col-span-3">
        <label class="flex items-center gap-2 text-sm"><input v-model="form.firstPurchase" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.firstPurchase") }}</label>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.onePerCustomer" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.onePerCustomer") }}</label>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.restore" type="checkbox" class="size-4" />{{ t("billing.pricing.promotions.codes.restore") }}</label>
      </div>
      <Button type="submit" :disabled="act.create.isPending.value">{{ t("billing.pricing.promotions.codes.create") }}</Button>
    </form>
    <p v-if="formError" class="text-destructive text-sm" role="alert">{{ formError }}</p>
    <p v-if="act.create.error.value" class="text-destructive text-sm" role="alert">{{ errorText(act.create.error.value) }}</p>
    <p v-if="act.deactivate.error.value" class="text-destructive text-sm" role="alert">{{ errorText(act.deactivate.error.value) }}</p>

    <div v-if="made.length" class="border-success/50 bg-success/10 grid gap-2 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("billing.pricing.promotions.codes.created", { n: made.length }) }}</p>
      <Textarea :model-value="made.join('\n')" readonly rows="5" class="mono max-w-md" :aria-label="t('billing.pricing.promotions.codes.createdList')" data-testid="made-codes" @focus="($event.target as HTMLTextAreaElement).select()" />
      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" @click="copyAll">{{ copied ? t("billing.pricing.promotions.codes.copied") : t("billing.pricing.promotions.codes.copyAll") }}</Button>
        <Button type="button" variant="outline" size="sm" @click="download">{{ t("billing.pricing.promotions.codes.download") }}</Button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-3">
      <Input v-model="search" class="w-56" :aria-label="t('billing.pricing.promotions.codes.search')" :placeholder="t('billing.pricing.promotions.codes.search')" @input="filter" />
      <NativeSelect v-model="state" class="w-40" :aria-label="t('billing.subscriptions.state')" @change="filter">
        <option value="">{{ t("billing.pricing.promotions.codes.stateAll") }}</option>
        <option value="active">{{ t("billing.pricing.coupons.active") }}</option>
        <option value="inactive">{{ t("billing.pricing.coupons.inactive") }}</option>
      </NativeSelect>
      <span class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.codes.total", { n: codes.data.value?.total ?? 0 }) }}</span>
    </div>
    <p v-if="codes.error.value" class="text-destructive text-sm" role="alert">{{ errorText(codes.error.value) }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.promotions.codes.used") }}</TableHead>
          <TableHead>{{ t("billing.pricing.promotions.codes.expiresCol") }}</TableHead>
          <TableHead>{{ t("billing.pricing.promotions.codes.rules") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in rows" :key="c.id">
          <TableCell class="mono font-medium">{{ c.code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ c.redeemed ?? 0 }}<template v-if="c.max_redemptions"> / {{ c.max_redemptions }}</template></TableCell>
          <TableCell class="text-xs">{{ when(c.expire_time) }}</TableCell>
          <TableCell class="text-xs">{{ rules(c).join(" · ") || "—" }}</TableCell>
          <TableCell><Badge :variant="c.active ? 'success' : 'secondary'">{{ c.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></TableCell>
          <TableCell class="text-end">
            <Button v-if="c.active" size="sm" variant="ghost" @click="act.deactivate.mutate(c.code ?? '')">{{ t("billing.pricing.coupons.deactivate") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!codes.isPending.value && rows.length === 0 && !codes.error.value" class="text-muted-foreground text-center text-sm">{{ t("billing.pricing.promotions.codes.none") }}</p>
    <div class="flex justify-end gap-2">
      <Button size="sm" variant="outline" :disabled="history.length === 0" @click="back">{{ t("billing.paymentLinks.previous") }}</Button>
      <Button size="sm" variant="outline" :disabled="!codes.data.value?.next_page_token" @click="next">{{ t("billing.paymentLinks.next") }}</Button>
    </div>
  </section>
</template>
