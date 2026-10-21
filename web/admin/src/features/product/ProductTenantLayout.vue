<script setup lang="ts">
import { Outlet, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useShell } from "@/app/layout/useShell";
import TenantHeader from "@/features/tenants/TenantHeader.vue";
import { useApps } from "./api";

/**
 * The deeper views of one environment of an app (customers, packages, subscriptions, ...): the console's own pages, reused as they are. They speak the
 * tenant API with the person's session token and the tenant as X-Quotify-Tenant, and the gateway lets that through only for a tenant of the person's
 * accounts. The sidebar is the navigation; this is the page head.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const shell = useShell();
const apps = useApps();
const tenant = computed(() => shell.located.value?.app.tenants?.find((x) => x.id === id.value));
const title = computed(() => (shell.current.value ? t(`shell.items.${shell.current.value.label}`) : "…"));
</script>

<template>
  <section class="flex flex-col">
    <TenantHeader :title="title" :name="shell.located.value?.app.name ?? (apps.isPending.value ? '…' : '')" :tenant="tenant" />
    <Outlet />
  </section>
</template>
