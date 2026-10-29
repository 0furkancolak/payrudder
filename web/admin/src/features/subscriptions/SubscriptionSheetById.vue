<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { usePackages } from "@/features/packages";
import { SubscriptionRelated } from "@/features/related";
import { useTenant } from "@/features/tenants";
import { envOf } from "@/shared/lib/environment";
import { firstError } from "@/shared/lib/errors";
import { operatorView } from "@/shared/lib/session";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { useSubscriptionActions, useSubscriptionById } from "./api";
import ManageSubscriptionCard from "./ManageSubscriptionCard.vue";

/**
 * A subscription's sheet opened over the sheet that mentions it (a customer's, an invoice's), so the reader is not taken to the subscriptions list: it stands
 * on its own, with no arrows, offers what the list's sheet offers (pause, cancel, change package…) and emits `close`.
 */
const props = defineProps<{ tenantId: string; subscriptionId: string }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();
const sub = useSubscriptionById(() => props.tenantId, () => props.subscriptionId);
const tenant = useTenant(() => props.tenantId);
const environment = computed(() => envOf(tenant.data.value?.environment));
const packages = usePackages(() => props.tenantId);
const act = useSubscriptionActions(() => props.tenantId);
const operator = computed(() => operatorView());
const failure = computed(() => firstError(...Object.values(act)));
</script>

<template>
  <DetailSheet :index="0" :total="1" :title="t('billing.subscriptions.managing', { customer: sub.data.value?.customer_external_id ?? '' })" :description="sub.data.value ? `${sub.data.value.plan_code} v${sub.data.value.plan_version}` : undefined" wide @update:index="emit('close')">
    <ErrorState :error="sub.error.value" :retrying="sub.isFetching.value" @retry="sub.refetch()" />
    <Skeleton v-if="sub.isPending.value" class="h-48 rounded-3xl" />
    <template v-else-if="sub.data.value">
      <ErrorText :error="failure" class="bg-destructive/10 mb-4 rounded-2xl px-4 py-3" />
      <ManageSubscriptionCard :tenant-id="tenantId" :subscription="sub.data.value" :packages="packages.data.value ?? []" :environment="environment" :operator="operator" :act="act" />
      <SubscriptionRelated v-if="sub.data.value.customer_external_id" :key="subscriptionId" :tenant-id="tenantId" :customer="sub.data.value.customer_external_id" class="mt-6" />
    </template>
  </DetailSheet>
</template>
