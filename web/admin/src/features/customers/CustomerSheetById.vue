<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { recordLink } from "@/features/related/link";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { emptySummary, type CustomerTab } from "./lib";
import { useCustomerById, useCustomerSummaries } from "./api";
import CustomerSheet from "./CustomerSheet.vue";

/**
 * A customer's sheet opened over the sheet that mentions them (an invoice, a payment, a subscription, a credit note), so the reader is not taken to the
 * customers page: it stands on its own, with no arrows, shows what the page's sheet shows and none of the forms, and emits `close`. A link at its foot goes to the
 * customers page for the ones who want to change something.
 */
const props = defineProps<{ tenantId: string; externalId: string }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();
const customer = useCustomerById(() => props.tenantId, () => props.externalId);
const summaries = useCustomerSummaries(() => props.tenantId);
const tab = ref<CustomerTab>("overview");
</script>

<template>
  <DetailSheet :index="0" :total="1" :title="customer.data.value?.name || externalId" :description="customer.data.value?.name ? externalId : undefined" wide @update:index="emit('close')">
    <ErrorState :error="customer.error.value" :retrying="customer.isFetching.value" @retry="customer.refetch()" />
    <Skeleton v-if="customer.isPending.value" class="h-48 rounded-3xl" />
    <div v-else-if="customer.data.value" class="flex flex-col gap-6">
      <CustomerSheet
        :key="externalId"
        v-model:tab="tab"
        :tenant-id="tenantId"
        :customer="customer.data.value"
        :summary="summaries.byCustomer.value.get(externalId) ?? emptySummary()"
        :summary-loading="summaries.loading.value"
        readonly
      />
      <Link v-bind="recordLink('customer', externalId, tenantId)" class="text-muted-foreground w-fit text-sm underline" data-testid="customer-peek-open-page">{{ t("related.openCustomerPage") }}</Link>
    </div>
  </DetailSheet>
</template>
