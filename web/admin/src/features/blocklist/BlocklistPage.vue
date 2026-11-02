<script setup lang="ts">
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import BlocklistTable from "./BlocklistTable.vue";

/** An app's own blocklist: who may not become a customer (or change to this address or tax id), kept by the owner. */
const tenantId = useRouteTenantId();
</script>

<template>
  <BlocklistTable :tenant-id="tenantId" />
</template>
