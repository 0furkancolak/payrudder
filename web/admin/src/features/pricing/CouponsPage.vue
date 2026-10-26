<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { useCoupons } from "./api";
import CodeSheet from "./CodeSheet.vue";
import CodesTab from "./CodesTab.vue";
import CouponsTab from "./CouponsTab.vue";
import RedemptionsTab from "./RedemptionsTab.vue";

/**
 * Coupons: what is taken off (the coupons), the codes customers type for it (promotion codes), and every use of either (redemptions). A tab each, and
 * the same shape in all three: a toolbar, a table, a sheet for the row. New promotion codes are made in one sheet that every tab opens.
 */
const TABS = ["coupons", "codes", "redemptions"] as const;
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tab = ref<(typeof TABS)[number]>("coupons");

const coupons = useCoupons(tenantId);
const active = computed(() => (coupons.data.value?.coupons ?? []).filter((c) => c.active));
const sheet = ref(false);
const preset = ref("");
// Codes made from a coupon are for that coupon; from the list of codes, any active coupon can be chosen.
const fixed = ref(false);
function newCode(coupon: string) {
  preset.value = coupon;
  fixed.value = tab.value === "coupons" && coupon !== "";
  sheet.value = true;
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <Tabs :model-value="tab" @update:model-value="tab = String($event) as (typeof TABS)[number]">
      <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('billing.coupons.tabsLabel')">
        <TabsTrigger v-for="x in TABS" :key="x" :value="x" class="flex-none" :data-testid="`coupons-tab-${x}`">{{ t(`billing.coupons.tabs.${x}`) }}</TabsTrigger>
      </TabsList>
    </Tabs>
    <p class="text-muted-foreground -mt-1 max-w-3xl px-1 text-sm">{{ t(`billing.coupons.hint.${tab}`) }}</p>

    <CouponsTab v-if="tab === 'coupons'" :tenant-id="tenantId" @new-code="newCode" />
    <CodesTab v-else-if="tab === 'codes'" :tenant-id="tenantId" @new-code="newCode" />
    <RedemptionsTab v-else :tenant-id="tenantId" />

    <CodeSheet v-model:open="sheet" :tenant-id="tenantId" :coupons="active" :preset="preset" :fixed="fixed" />
  </div>
</template>
