<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { useTabParam } from "@/shared/lib/useSearchValue";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import CommissionsTab from "./CommissionsTab.vue";
import JournalTab from "./JournalTab.vue";
import OverviewTab from "./OverviewTab.vue";
import PartnersTab from "./PartnersTab.vue";
import PayoutsTab from "./PayoutsTab.vue";
import ProgramTab from "./ProgramTab.vue";
import { useRouteTenantId } from "@/shared/lib/routeTenant";

/**
 * Growth > Affiliates: the partner program of one environment. One tab per job; each tab is its own component and asks the API only while it is open.
 * The page works for a person (their own app) and for the operator, who both speak the same key-scoped /v1/affiliates API.
 */
const TABS = ["overview", "program", "partners", "commissions", "payouts", "journal"] as const;
type Tab = (typeof TABS)[number];

const { t } = useI18n();
const tenantId = useRouteTenantId();
const tab = useTabParam<Tab>("tab", TABS, "overview");
/** A payout another tab sends the reader to (the payout of a commission). */
const focusPayout = ref("");

function show(next: string) {
  if ((TABS as readonly string[]).includes(next)) tab.value = next as Tab;
}
function openPayout(id: string) {
  focusPayout.value = id;
  tab.value = "payouts";
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <p class="text-muted-foreground text-sm">{{ t("affiliates.intro") }}</p>
    <Tabs :model-value="tab" @update:model-value="show(String($event))">
      <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('affiliates.tabsLabel')">
        <TabsTrigger v-for="x in TABS" :key="x" :value="x" class="flex-none">{{ t(`affiliates.tabs.${x}`) }}</TabsTrigger>
      </TabsList>
    </Tabs>
    <OverviewTab v-if="tab === 'overview'" :tenant-id="tenantId" @goto="show" />
    <ProgramTab v-else-if="tab === 'program'" :tenant-id="tenantId" />
    <PartnersTab v-else-if="tab === 'partners'" :tenant-id="tenantId" />
    <CommissionsTab v-else-if="tab === 'commissions'" :tenant-id="tenantId" @payout="openPayout" />
    <PayoutsTab v-else-if="tab === 'payouts'" :key="focusPayout" :tenant-id="tenantId" :focus="focusPayout" />
    <JournalTab v-else :tenant-id="tenantId" />
  </div>
</template>
