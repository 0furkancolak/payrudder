<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useRouteTenantId } from "@/shared/lib/routeTenant";
import AuditLog from "./AuditLog.vue";

/** The audit log of one tenant (an app's environment). Who may read it is decided by the API: an owner or administrator, or a key with audit:read. */
const { t } = useI18n();
const tenantId = useRouteTenantId();
</script>

<template>
  <p class="text-muted-foreground mb-4 max-w-2xl px-1 text-sm">{{ t("audit.intro") }}</p>
  <AuditLog mode="tenant" :tenant-id="tenantId" />
</template>
