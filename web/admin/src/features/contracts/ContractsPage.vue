<script setup lang="ts">
import { Scale } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import { useTabParam } from "@/shared/lib/useSearchValue";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import AgreementsCard from "./AgreementsCard.vue";
import SettingsCard from "./SettingsCard.vue";
import WithdrawalsCard from "./WithdrawalsCard.vue";

/**
 * The agreements a seller presents to its own customers (distance sales contract, pre-information, terms, privacy notice), what it requires of them, and
 * the withdrawal requests that come back. Payrudder supplies templates and the evidence of acceptance, never legal advice; the page says so first.
 */
const TABS = ["agreements", "settings", "withdrawals"] as const;
const { t } = useI18n();
const tenantId = useRouteTenantId();
const tab = useTabParam("tab", TABS, "agreements");
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="bg-muted text-muted-foreground flex items-start gap-3 rounded-2xl px-4 py-3 text-sm" data-testid="contracts-disclaimer">
      <Scale class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <p class="m-0">{{ t("contracts.disclaimer") }}</p>
    </div>
    <Tabs :model-value="tab" @update:model-value="tab = String($event) as (typeof TABS)[number]">
      <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('contracts.tabsLabel')">
        <TabsTrigger v-for="x in TABS" :key="x" :value="x" class="flex-none" :data-testid="`contracts-tab-${x}`">{{ t(`contracts.tabs.${x}`) }}</TabsTrigger>
      </TabsList>
    </Tabs>
    <AgreementsCard v-if="tab === 'agreements'" :tenant-id="tenantId" />
    <SettingsCard v-else-if="tab === 'settings'" :tenant-id="tenantId" />
    <WithdrawalsCard v-else :tenant-id="tenantId" />
  </div>
</template>
