<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { thisMonth } from "@/shared/lib/dateRange";
import { useTabParam } from "@/shared/lib/useSearchValue";
import type { DateRange } from "@/shared/ui/date-range";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import CustomersTab from "./CustomersTab.vue";
import { periodOf, validRange } from "./lib";
import MetersTab from "./MetersTab.vue";
import OverviewTab from "./OverviewTab.vue";
import PeriodControls from "./PeriodControls.vue";

/**
 * Usage, for a person's app and for the operator's tenant (the same page, the tenant comes from the route): an overview of the meters, the meters
 * themselves, and the customers with their limits. The period chosen here is shared by the three.
 */
const TABS = ["overview", "meters", "customers"] as const;
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tab = useTabParam("tab", TABS, "overview");

const range = ref<DateRange>(thisMonth());
const compare = ref(true);
// An empty or too long range is not asked of the server: the last 30 days stand in, and the controls say so.
const effective = computed(() => (validRange(range.value) ? range.value : thisMonth()));
const tooLong = computed(() => range.value.from !== "" && range.value.to !== "" && !validRange(range.value));
const period = computed(() => periodOf(effective.value.to));
</script>

<template>
  <div class="flex flex-col gap-4">
    <Tabs :model-value="tab" @update:model-value="tab = String($event) as (typeof TABS)[number]">
      <TabsList class="h-auto w-full flex-wrap justify-start" :aria-label="t('billing.usage.tabsLabel')">
        <TabsTrigger v-for="x in TABS" :key="x" :value="x" class="flex-none" :data-testid="`usage-tab-${x}`">{{ t(`billing.usage.tabs.${x}`) }}</TabsTrigger>
      </TabsList>
    </Tabs>

    <template v-if="tab === 'overview'">
      <div class="flex flex-wrap items-center gap-3"><PeriodControls v-model:range="range" v-model:compare="compare" :period="period" :too-long="tooLong" /></div>
      <OverviewTab :tenant-id="tenantId" :range="effective" :compare="compare" :period="period" />
    </template>
    <MetersTab v-else-if="tab === 'meters'" v-model:range="range" v-model:compare="compare" :tenant-id="tenantId" :effective="effective" :period="period" :too-long="tooLong" />
    <CustomersTab v-else v-model:range="range" v-model:compare="compare" :tenant-id="tenantId" :effective="effective" :period="period" :too-long="tooLong" />
  </div>
</template>
