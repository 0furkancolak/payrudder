<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { idOf } from "@/shared/lib/sheets";
import { useSearchValue } from "@/shared/lib/useSearchValue";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { useCreditPacks } from "./api";
import CreditPacksCard from "./CreditPacksCard.vue";
import CustomerCreditsCard from "./CustomerCreditsCard.vue";
import LowBalanceCard from "./LowBalanceCard.vue";
import PurchaseLinkCard from "./PurchaseLinkCard.vue";

/**
 * Prepaid credits of a tenant. First one customer's balance with the grants it comes from and its ledger (and the sheet to give free credits); then, as a
 * secondary section, what is needed to sell credits: the packs (with a purchase link per pack) and the low-balance alert.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
// The customer picked is in the address, so a reload or a shared link shows the same balance.
const customer = useSearchValue("customer");
// The grant sheet is in the address with the customer it is for: a reload or a shared link chooses that customer again.
watch(
  () => idOf("grant-credits"),
  (named) => {
    if (named !== undefined && customer.value === "") customer.value = decodeURIComponent(named);
  },
  { immediate: true },
);

const packs = useCreditPacks(tenantId);
const linkPack = ref<{ id: string; name: string } | null>(null);
const linkOpen = computed({
  get: () => linkPack.value !== null,
  set: (v) => {
    if (!v) linkPack.value = null;
  },
});
useSheetTarget("purchase-link", linkOpen, linkPack, {
  id: (p) => p.id,
  find: (id) => {
    const p = (packs.data.value?.packs ?? []).find((x) => x.id === id && x.active);
    return p ? { id, name: p.name ?? "" } : undefined;
  },
});
</script>

<template>
  <CustomerCreditsCard v-model:customer="customer" :tenant-id="tenantId" />

  <section class="mb-6 grid gap-3" aria-labelledby="credits-sell-title" data-testid="credits-sell">
    <div class="px-1">
      <h2 id="credits-sell-title" class="text-base font-semibold">{{ t("billing.credits.sell.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.credits.sell.hint") }}</p>
    </div>
    <CreditPacksCard :tenant-id="tenantId" @purchase-link="linkPack = $event" />
    <LowBalanceCard :tenant-id="tenantId" />
  </section>
  <PurchaseLinkCard :tenant-id="tenantId" :pack="linkPack" @close="linkPack = null" />
</template>
