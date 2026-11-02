<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { PageHeader } from "@/shared/ui/page-header";
import AuditLog from "./AuditLog.vue";

/** The operator's audit log across all tenants, with a filter for one tenant. */
const { t } = useI18n();
</script>

<template>
  <PageHeader :title="t('shell.items.auditLog')" :description="t('audit.adminIntro')" />
  <AuditLog mode="operator" />
</template>
