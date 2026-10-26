<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed, ref } from "vue";
import { usePackages } from "@/features/packages";
import { useTenant } from "@/features/tenants";
import { envOf } from "@/shared/lib/environment";
import { firstError } from "@/shared/lib/errors";
import { operatorView } from "@/shared/lib/session";
import { ErrorText } from "@/shared/ui/error-text";
import { useSubscriptionActions } from "./api";
import CreateSubscriptionForm from "./CreateSubscriptionForm.vue";
import ManageSubscriptionCard from "./ManageSubscriptionCard.vue";
import SubscriptionsTable from "./SubscriptionsTable.vue";

/** Subscriptions of a tenant: start one, see them all, and manage the one a row opens in a sheet. Operators also move a subscription to another payment provider. */
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(tenantId);
const environment = computed(() => envOf(tenant.data.value?.environment));
const packages = usePackages(tenantId);
const act = useSubscriptionActions(tenantId);

const creating = ref(false);
/** Moving a subscription to another payment provider, and driving one a provider runs, go through the provider accounts: operator actions. */
const operator = computed(() => operatorView());
const failure = computed(() => firstError(...Object.values(act)));
</script>

<template>
  <CreateSubscriptionForm v-model:open="creating" :tenant-id="tenantId" :packages="packages.data.value ?? []" :act="act" />
  <ErrorText :error="failure" class="bg-destructive/10 mb-4 rounded-2xl px-4 py-3" />
  <SubscriptionsTable :tenant-id="tenantId" @create="creating = true">
    <template #detail="{ subscription }">
      <ManageSubscriptionCard :tenant-id="tenantId" :subscription="subscription" :packages="packages.data.value ?? []" :environment="environment" :operator="operator" :act="act" />
    </template>
  </SubscriptionsTable>
</template>
