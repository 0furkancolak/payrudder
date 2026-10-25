<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useTenantCurrency } from "@/features/tenants/currency";
import { FALLBACK_CURRENCY } from "@/shared/lib/currencies";
import { useFormat } from "@/shared/lib/format";
import { parseMajor } from "@/shared/lib/money";
import { CREDITS_RE, CREDIT_VALIDITY_MAX_DAYS } from "@/shared/lib/rules";
import { useSeededRef } from "@/shared/lib/seeded";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { CurrencySelect } from "@/shared/ui/search-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreateCreditPack, useCreditPacks, useSetCreditPackActive } from "./api";
import { formatCredits, validityDays } from "./lib";

/** The packs of credits a tenant sells: a form to add one (priced in major units) and the table of them, each of which can be withdrawn from sale or offered by a purchase link. */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ purchaseLink: [pack: { id: string; name: string }] }>();
const { t, locale } = useI18n();
const fmt = useFormat();

const packs = useCreditPacks(() => props.tenantId);
const createPack = useCreateCreditPack(() => props.tenantId);
const setActive = useSetCreditPackActive(() => props.tenantId);
const packName = ref("");
const packCredits = ref("");
const packPrice = ref("");
// The pack is priced in the tenant's own currency unless the person picks another one.
const packCurrency = useSeededRef<string>(useTenantCurrency(() => props.tenantId), FALLBACK_CURRENCY);
const packValidity = ref("");
const priceMinor = computed(() => parseMajor(packPrice.value, packCurrency.value));
const creditsValid = computed(() => CREDITS_RE.test(packCredits.value.trim()));
const validity = computed(() => validityDays(packValidity.value, CREDIT_VALIDITY_MAX_DAYS));
const validityValid = computed(() => validity.value !== undefined);

function submitPack() {
  const days = validity.value;
  if (!priceMinor.value || !creditsValid.value || days === undefined) return;
  createPack.mutate(
    { name: packName.value.trim(), credits: packCredits.value.trim(), price_minor: priceMinor.value, currency: packCurrency.value, ...(days !== null ? { validity_days: days } : {}) },
    {
      onSuccess: () => {
        packName.value = packCredits.value = packPrice.value = packValidity.value = "";
      },
    },
  );
}
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
          <TableCell class="text-end">{{ formatCredits(p.credits, locale) }}</TableCell>
          <TableCell class="text-end">{{ fmt.money(p.price_minor, p.currency) }}</TableCell>
          <TableCell>{{ p.validity_days ? t("billing.credits.packs.days", { n: p.validity_days }) : t("billing.credits.packs.noExpiry") }}</TableCell>
          <TableCell>
            <Badge :variant="p.active ? 'success' : 'secondary'">{{ p.active ? t("billing.credits.packs.onSale") : t("billing.credits.packs.withdrawn") }}</Badge>
          </TableCell>
          <TableCell class="space-x-2 text-end">
            <Button v-if="p.active" type="button" size="sm" @click="emit('purchaseLink', { id: p.id ?? '', name: p.name ?? '' })">{{ t("billing.credits.packs.purchaseLink") }}</Button>
            <Button type="button" variant="outline" size="sm" :disabled="setActive.isPending.value" @click="setActive.mutate({ id: p.id ?? '', active: !p.active })">
              {{ p.active ? t("billing.credits.packs.withdraw") : t("billing.credits.packs.restore") }}
            </Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <ErrorText :error="setActive.error.value" class="px-3" />
  </Card>
</template>
