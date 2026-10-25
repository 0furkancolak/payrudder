<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCustomers } from "@/features/customers/api";
import { absoluteUrl } from "@/features/paymentlinks/lib";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { useTenantCurrency } from "@/features/tenants/currency";
import { ErrorText } from "@/shared/ui/error-text";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { CurrencySelect } from "@/shared/ui/search-select";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import {
  useCreateCreditPack,
  useCreatePurchaseLink,
  useCreditBalance,
  useCreditLedger,
  useCreditPacks,
  useCreditSettings,
  useGrantCredits,
  useSetCreditPackActive,
  useSetCreditSettings,
} from "./api";

const { t, locale } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const number = computed(() => new Intl.NumberFormat(locale.value));
const credits = (v: string | number | undefined) => (v === undefined || v === "" ? "" : number.value.format(BigInt(v)));
const when = (iso?: string | null) => (iso ? new Date(iso).toLocaleString(locale.value) : "");

// --- packs
const packs = useCreditPacks(tenantId);
const createPack = useCreateCreditPack(tenantId);
const setActive = useSetCreditPackActive(tenantId);
const packName = ref("");
const packCredits = ref("");
const packPrice = ref("");
const packCurrency = ref<string>(FALLBACK_CURRENCY);
// The pack is priced in the tenant's own currency unless the person picks another one.
const tenantCurrency = useTenantCurrency(tenantId);
let currencySeeded = false;
watch(
  tenantCurrency,
  (c) => {
    if (c && !currencySeeded) {
      currencySeeded = true;
      packCurrency.value = c;
    }
  },
  { immediate: true },
);
const priceMinor = computed(() => parseMajor(packPrice.value, packCurrency.value));
const packValidity = ref("");
const creditsValid = computed(() => /^[1-9]\d{0,11}$/.test(packCredits.value.trim()));
const validityValid = computed(() => packValidity.value.trim() === "" || (/^\d{1,4}$/.test(packValidity.value.trim()) && Number(packValidity.value) >= 1 && Number(packValidity.value) <= 3650));
function submitPack() {
  if (!priceMinor.value || !creditsValid.value || !validityValid.value) return;
  const days = Number(packValidity.value.trim());
  createPack.mutate(
    { name: packName.value.trim(), credits: packCredits.value.trim(), price_minor: priceMinor.value, currency: packCurrency.value, ...(days > 0 ? { validity_days: days } : {}) },
    {
      onSuccess: () => {
        packName.value = packCredits.value = packPrice.value = packValidity.value = "";
      },
    },
  );
}

// --- a purchase link for one pack: the customer opens it to buy
const linkPack = ref<{ id: string; name: string } | null>(null);
const linkCustomer = ref("");
const linkQuantity = ref("1");
const linkPromotion = ref("");
const linkKey = ref(crypto.randomUUID());
const createLink = useCreatePurchaseLink(tenantId);
const quantityValid = computed(() => Number.isInteger(Number(linkQuantity.value)) && Number(linkQuantity.value) >= 1 && Number(linkQuantity.value) <= 1000);
/** The URL is returned once, right after creation, and is not kept anywhere else. */
const created = computed(() => createLink.data.value as { url?: string; replayed?: boolean } | undefined);
const shownUrl = computed(() => absoluteUrl(created.value?.url, window.location.origin));
function openLink(p: { id?: string; name?: string }) {
  createLink.reset();
  linkPack.value = { id: p.id ?? "", name: p.name ?? "" };
  linkKey.value = crypto.randomUUID();
  linkPromotion.value = "";
}
function submitLink() {
  if (!linkPack.value || !linkCustomer.value || !quantityValid.value) return;
  createLink.mutate({ packId: linkPack.value.id, customer: linkCustomer.value, quantity: Number(linkQuantity.value), key: linkKey.value, promotion: linkPromotion.value });
}
async function copyLink() {
  try {
    await navigator.clipboard.writeText(shownUrl.value);
  } catch {
    /* the field is selectable */
  }
}

// --- the low-balance alert
const settings = useCreditSettings(tenantId);
const setSettings = useSetCreditSettings(tenantId);
const threshold = ref("");
watch(
  () => settings.data.value?.low_threshold,
  (v) => {
    threshold.value = v && v !== "0" ? v : "";
  },
  { immediate: true },
);
const thresholdValid = computed(() => threshold.value.trim() === "" || /^(0|[1-9]\d{0,11})$/.test(threshold.value.trim()));
function saveThreshold() {
  if (thresholdValid.value) setSettings.mutate(threshold.value.trim() || "0");
}

// --- one customer's credits
const customers = useCustomers(tenantId);
const customer = ref("");
const balance = useCreditBalance(tenantId, customer);
const ledger = useCreditLedger(tenantId, customer);
const grant = useGrantCredits(tenantId);
const grantCredits = ref("");
const grantSource = ref<"promo" | "manual">("promo");
const grantExpiry = ref("");
const grantReason = ref("");
const grantValid = computed(() => customer.value !== "" && /^[1-9]\d{0,11}$/.test(grantCredits.value.trim()));
function submitGrant() {
  if (!grantValid.value) return;
  grant.mutate(
    {
      customer: customer.value,
      credits: grantCredits.value.trim(),
      source: grantSource.value,
      // The end of the chosen day, in the operator's time zone.
      expireTime: grantExpiry.value ? new Date(`${grantExpiry.value}T23:59:59`).toISOString() : undefined,
      reason: grantReason.value.trim() || undefined,
    },
    {
      onSuccess: () => {
        grantCredits.value = grantExpiry.value = grantReason.value = "";
      },
    },
  );
}
const kindVariant = (kind?: string) => (kind === "grant" ? "success" : kind === "expire" ? "secondary" : "outline");
</script>

<template>
  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.packs.new") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.credits.packs.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-6" role="form" :aria-label="t('billing.credits.packs.formLabel')" @submit.prevent="submitPack">
      <Input v-model="packName" :aria-label="t('billing.credits.packs.name')" :placeholder="t('billing.credits.packs.name')" maxlength="80" required />
      <Input v-model="packCredits" inputmode="numeric" :aria-label="t('billing.credits.packs.credits')" :placeholder="t('billing.credits.packs.credits')" required />
      <Input v-model="packPrice" inputmode="decimal" :aria-label="t('billing.credits.packs.price')" :placeholder="t('billing.credits.packs.price')" required />
      <CurrencySelect v-model="packCurrency" :aria-label="t('billing.credits.packs.currency')" />
      <Input v-model="packValidity" inputmode="numeric" :aria-label="t('billing.credits.packs.validity')" :placeholder="t('billing.credits.packs.validity')" />
      <Button type="submit" :disabled="createPack.isPending.value || !priceMinor || !creditsValid || !validityValid">{{ t("billing.credits.packs.create") }}</Button>
    </form>
    <ErrorText :error="createPack.error.value" />
  </Card>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.packs.title") }}</h2>
    <ErrorText :error="packs.error.value" class="px-3" />
    <p v-if="packs.data.value && !(packs.data.value.packs ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.packs.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.packs.name") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.packs.credits") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.packs.price") }}</TableHead>
          <TableHead>{{ t("billing.credits.packs.validityCol") }}</TableHead>
          <TableHead>{{ t("billing.credits.packs.state") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in packs.data.value?.packs ?? []" :key="p.id">
          <TableCell class="font-medium">{{ p.name }}</TableCell>
          <TableCell class="text-end">{{ credits(p.credits) }}</TableCell>
          <TableCell class="text-end">{{ formatMinor(p.price_minor, p.currency, locale) }}</TableCell>
          <TableCell>{{ p.validity_days ? t("billing.credits.packs.days", { n: p.validity_days }) : t("billing.credits.packs.noExpiry") }}</TableCell>
          <TableCell>
            <Badge :variant="p.active ? 'success' : 'secondary'">{{ p.active ? t("billing.credits.packs.onSale") : t("billing.credits.packs.withdrawn") }}</Badge>
          </TableCell>
          <TableCell class="space-x-2 text-end">
            <Button v-if="p.active" type="button" size="sm" @click="openLink(p)">{{ t("billing.credits.packs.purchaseLink") }}</Button>
            <Button type="button" variant="outline" size="sm" :disabled="setActive.isPending.value" @click="setActive.mutate({ id: p.id ?? '', active: !p.active })">
              {{ p.active ? t("billing.credits.packs.withdraw") : t("billing.credits.packs.restore") }}
            </Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <ErrorText :error="setActive.error.value" class="px-3" />
  </Card>

  <Card v-if="linkPack" class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.purchase.title") }}: {{ linkPack.name }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.credits.purchase.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="form" :aria-label="t('billing.credits.purchase.formLabel')" @submit.prevent="submitLink">
      <NativeSelect v-model="linkCustomer" :aria-label="t('billing.credits.purchase.customer')" required>
        <option value="">{{ t("billing.credits.purchase.pickCustomer") }}</option>
        <option v-for="c in customers.data.value?.customers ?? []" :key="c.id" :value="c.external_id">{{ c.name }} ({{ c.external_id }})</option>
      </NativeSelect>
      <Input v-model="linkQuantity" inputmode="numeric" :aria-label="t('billing.credits.purchase.quantity')" :placeholder="t('billing.credits.purchase.quantity')" />
      <Input v-model="linkPromotion" maxlength="40" :aria-label="t('billing.credits.purchase.promotionCode')" :placeholder="t('billing.credits.purchase.promotionCode')" :title="t('billing.paymentLinks.promotionHint')" />
      <Button type="submit" :disabled="createLink.isPending.value || !linkCustomer || !quantityValid">{{ t("billing.credits.purchase.create") }}</Button>
      <Button type="button" variant="outline" @click="linkPack = null">{{ t("billing.credits.purchase.close") }}</Button>
    </form>
    <ErrorText :error="createLink.error.value" />
    <div v-if="created && createLink.isSuccess.value && shownUrl" class="border-warning/50 bg-warning/10 grid gap-2 rounded-md border p-3" role="status">
      <p class="text-sm font-medium">{{ t("billing.credits.purchase.shownOnce") }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <Input :model-value="shownUrl" readonly class="mono max-w-xl" :aria-label="t('billing.credits.purchase.url')" data-testid="purchase-link-url" @focus="($event.target as HTMLInputElement).select()" />
        <Button type="button" variant="outline" @click="copyLink">{{ t("billing.credits.purchase.copy") }}</Button>
      </div>
    </div>
  </Card>

  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.settings.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("billing.credits.settings.hint") }}</p>
    <form class="grid gap-3 sm:grid-cols-3" role="form" :aria-label="t('billing.credits.settings.formLabel')" @submit.prevent="saveThreshold">
      <Input v-model="threshold" inputmode="numeric" :aria-label="t('billing.credits.settings.threshold')" :placeholder="t('billing.credits.settings.threshold')" />
      <Button type="submit" :disabled="setSettings.isPending.value || !thresholdValid">{{ t("billing.credits.settings.save") }}</Button>
      <span v-if="setSettings.isSuccess.value" class="text-muted-foreground self-center text-sm" role="status">{{ t("billing.credits.settings.saved") }}</span>
    </form>
    <ErrorText :error="setSettings.error.value" />
  </Card>

  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.customer.title") }}</h2>
    <NativeSelect v-model="customer" :aria-label="t('billing.credits.customer.pick')">
      <option value="">{{ t("billing.credits.customer.pick") }}</option>
      <option v-for="c in customers.data.value?.customers ?? []" :key="c.id" :value="c.external_id">{{ c.name }} ({{ c.external_id }})</option>
    </NativeSelect>

    <template v-if="customer">
      <ErrorText :error="balance.error.value" />
      <div v-if="balance.data.value" class="flex flex-wrap items-baseline gap-x-4 gap-y-1" role="status" :aria-label="t('billing.credits.customer.balance')">
        <span class="text-3xl font-semibold tabular-nums">{{ credits(balance.data.value.balance) }}</span>
        <span class="text-muted-foreground text-sm">{{ t("billing.credits.customer.balance") }}</span>
        <span v-if="balance.data.value.next_expire_time" class="text-muted-foreground text-sm">
          {{ t("billing.credits.customer.nextExpiry", { when: when(balance.data.value.next_expire_time) }) }}
        </span>
      </div>

      <Table v-if="(balance.data.value?.lots ?? []).length">
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.credits.lots.source") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.remaining") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.granted") }}</TableHead>
            <TableHead>{{ t("billing.credits.lots.expires") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="l in balance.data.value?.lots ?? []" :key="l.id">
            <TableCell>{{ t(`billing.credits.source.${l.source}`) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.remaining) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.granted) }}</TableCell>
            <TableCell>{{ l.expire_time ? when(l.expire_time) : t("billing.credits.lots.never") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>

      <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5" role="form" :aria-label="t('billing.credits.grant.formLabel')" @submit.prevent="submitGrant">
        <Input v-model="grantCredits" inputmode="numeric" :aria-label="t('billing.credits.grant.credits')" :placeholder="t('billing.credits.grant.credits')" required />
        <NativeSelect v-model="grantSource" :aria-label="t('billing.credits.grant.source')">
          <option value="promo">{{ t("billing.credits.source.promo") }}</option>
          <option value="manual">{{ t("billing.credits.source.manual") }}</option>
        </NativeSelect>
        <Input v-model="grantExpiry" type="date" :aria-label="t('billing.credits.grant.expiry')" :title="t('billing.credits.grant.expiryHint')" />
        <Input v-model="grantReason" maxlength="200" :aria-label="t('billing.credits.grant.reason')" :placeholder="t('billing.credits.grant.reason')" />
        <Button type="submit" :disabled="grant.isPending.value || !grantValid">{{ t("billing.credits.grant.submit") }}</Button>
      </form>
      <ErrorText :error="grant.error.value" />
    </template>
  </Card>

  <Card v-if="customer" class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.ledger.title") }}</h2>
    <ErrorText :error="ledger.error.value" class="px-3" />
    <p v-if="ledger.data.value && !(ledger.data.value.entries ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.ledger.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.ledger.time") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.kind") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.source") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.ledger.amount") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.reason") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="e in ledger.data.value?.entries ?? []" :key="e.id">
          <TableCell>{{ when(e.create_time) }}</TableCell>
          <TableCell>
            <Badge :variant="kindVariant(e.kind)">{{ t(`billing.credits.kind.${e.kind}`) }}</Badge>
          </TableCell>
          <TableCell>
            <Badge v-if="e.source === 'purchase'" variant="success">{{ t("billing.credits.source.purchase") }}</Badge>
            <span v-else-if="e.source" class="text-muted-foreground text-sm">{{ t(`billing.credits.source.${e.source}`) }}</span>
          </TableCell>
          <TableCell class="text-end tabular-nums">{{ credits(e.amount) }}</TableCell>
          <TableCell class="text-muted-foreground">{{ e.reason }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
