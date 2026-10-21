<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCustomers } from "@/features/customers/api";
import { formatMinor, parseMajor } from "@/shared/lib/money";
import { useErrorText } from "@/shared/lib/errors";
import { CURRENCIES } from "@/shared/lib/currencies";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreateCreditPack, useCreditBalance, useCreditLedger, useCreditPacks, useGrantCredits, useSetCreditPackActive } from "./api";

const { t, locale } = useI18n();
const errorText = useErrorText();
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
const packCurrency = ref<string>("USD");
const priceMinor = computed(() => parseMajor(packPrice.value, packCurrency.value));
const creditsValid = computed(() => /^[1-9]\d{0,11}$/.test(packCredits.value.trim()));
function submitPack() {
  if (!priceMinor.value || !creditsValid.value) return;
  createPack.mutate(
    { name: packName.value.trim(), credits: packCredits.value.trim(), price_minor: priceMinor.value, currency: packCurrency.value },
    {
      onSuccess: () => {
        packName.value = packCredits.value = packPrice.value = "";
      },
    },
  );
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
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5" role="form" :aria-label="t('billing.credits.packs.formLabel')" @submit.prevent="submitPack">
      <Input v-model="packName" :aria-label="t('billing.credits.packs.name')" :placeholder="t('billing.credits.packs.name')" maxlength="80" required />
      <Input v-model="packCredits" inputmode="numeric" :aria-label="t('billing.credits.packs.credits')" :placeholder="t('billing.credits.packs.credits')" required />
      <Input v-model="packPrice" inputmode="decimal" :aria-label="t('billing.credits.packs.price')" :placeholder="t('billing.credits.packs.price')" required />
      <NativeSelect v-model="packCurrency" :aria-label="t('billing.credits.packs.currency')">
        <option v-for="c in CURRENCIES" :key="c" :value="c">{{ c }}</option>
      </NativeSelect>
      <Button type="submit" :disabled="createPack.isPending.value || !priceMinor || !creditsValid">{{ t("billing.credits.packs.create") }}</Button>
    </form>
    <p v-if="createPack.error.value" class="text-destructive text-sm" role="alert">{{ errorText(createPack.error.value) }}</p>
  </Card>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.packs.title") }}</h2>
    <p v-if="packs.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(packs.error.value) }}</p>
    <p v-if="packs.data.value && !(packs.data.value.packs ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.packs.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.packs.name") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.packs.credits") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.credits.packs.price") }}</TableHead>
          <TableHead>{{ t("billing.credits.packs.state") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in packs.data.value?.packs ?? []" :key="p.id">
          <TableCell class="font-medium">{{ p.name }}</TableCell>
          <TableCell class="text-end">{{ credits(p.credits) }}</TableCell>
          <TableCell class="text-end">{{ formatMinor(p.price_minor, p.currency, locale) }}</TableCell>
          <TableCell>
            <Badge :variant="p.active ? 'success' : 'secondary'">{{ p.active ? t("billing.credits.packs.onSale") : t("billing.credits.packs.withdrawn") }}</Badge>
          </TableCell>
          <TableCell class="text-end">
            <Button type="button" variant="outline" size="sm" :disabled="setActive.isPending.value" @click="setActive.mutate({ id: p.id ?? '', active: !p.active })">
              {{ p.active ? t("billing.credits.packs.withdraw") : t("billing.credits.packs.restore") }}
            </Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="setActive.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(setActive.error.value) }}</p>
  </Card>

  <Card class="mb-6">
    <h2 class="text-lg font-semibold">{{ t("billing.credits.customer.title") }}</h2>
    <NativeSelect v-model="customer" :aria-label="t('billing.credits.customer.pick')">
      <option value="">{{ t("billing.credits.customer.pick") }}</option>
      <option v-for="c in customers.data.value?.customers ?? []" :key="c.id" :value="c.external_id">{{ c.name }} ({{ c.external_id }})</option>
    </NativeSelect>

    <template v-if="customer">
      <p v-if="balance.error.value" class="text-destructive text-sm" role="alert">{{ errorText(balance.error.value) }}</p>
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
      <p v-if="grant.error.value" class="text-destructive text-sm" role="alert">{{ errorText(grant.error.value) }}</p>
    </template>
  </Card>

  <Card v-if="customer" class="p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("billing.credits.ledger.title") }}</h2>
    <p v-if="ledger.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(ledger.error.value) }}</p>
    <p v-if="ledger.data.value && !(ledger.data.value.entries ?? []).length" class="text-muted-foreground px-3 py-2 text-sm">{{ t("billing.credits.ledger.none") }}</p>
    <Table v-else>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.credits.ledger.time") }}</TableHead>
          <TableHead>{{ t("billing.credits.ledger.kind") }}</TableHead>
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
          <TableCell class="text-end tabular-nums">{{ credits(e.amount) }}</TableCell>
          <TableCell class="text-muted-foreground">{{ e.reason }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
