<script setup lang="ts">
import { Outlet, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useShell } from "@/app/layout/useShell";
import TenantHeader from "./TenantHeader.vue";
import { ErrorState } from "@/shared/ui/error-state";

/** One tenant for the operator. The sidebar lists the tenant's pages; this is the page head and the place the tenant's own error shows. */
const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const shell = useShell();
const tenant = shell.tenant;
const title = computed(() => (shell.current.value && shell.current.value.key !== "dashboard" ? t(`shell.items.${shell.current.value.label}`) : (tenant.data.value?.name ?? t("tenant.fallbackTitle"))));
</script>

<template>
  <TenantHeader :title="title" :name="shell.current.value?.key !== 'dashboard' ? (tenant.data.value?.name ?? '') : ''" :tenant="tenant.data.value" />
  <ErrorState :error="tenant.error.value" :retrying="tenant.isFetching.value" class="mb-4" @retry="tenant.refetch()" />
  <p class="mono text-muted-foreground -mt-4 mb-4 px-1 text-xs" data-testid="tenant-id">{{ id }}</p>
  <Outlet />
</template>
