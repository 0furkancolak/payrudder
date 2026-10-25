<script setup lang="ts">
import { useI18n } from "vue-i18n";
import CommissionsTable from "./CommissionsTable.vue";

defineProps<{ tenantId: string }>();
defineEmits<{ payout: [id: string] }>();
const { t } = useI18n();
</script>

<template>
  <div>
    <h2 class="text-lg font-semibold">{{ t("affiliates.commissions.title") }}</h2>
    <p class="text-muted-foreground text-sm">{{ t("affiliates.commissions.intro") }}</p>
  </div>
  <CommissionsTable :tenant-id="tenantId" @payout="$emit('payout', $event)" />
</template>
