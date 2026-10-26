<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import CreditPacksCard from "./CreditPacksCard.vue";
import CustomerCreditsCard from "./CustomerCreditsCard.vue";
import LedgerCard from "./LedgerCard.vue";
import LowBalanceCard from "./LowBalanceCard.vue";
import PurchaseLinkCard from "./PurchaseLinkCard.vue";

/** Prepaid credits of a tenant: the packs it sells (with a purchase link per pack), one customer's balance, grants and ledger, and last the low-balance alert. */
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const linkPack = ref<{ id: string; name: string } | null>(null);
const customer = ref("");
</script>

<template>
  <CreditPacksCard :tenant-id="tenantId" @purchase-link="linkPack = $event" />
  <PurchaseLinkCard :tenant-id="tenantId" :pack="linkPack" @close="linkPack = null" />
  <CustomerCreditsCard v-model:customer="customer" :tenant-id="tenantId" />
  <LedgerCard v-if="customer" :tenant-id="tenantId" :customer="customer" class="mb-6" />
  <LowBalanceCard :tenant-id="tenantId" />
</template>
