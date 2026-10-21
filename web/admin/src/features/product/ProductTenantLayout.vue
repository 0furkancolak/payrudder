<script setup lang="ts">
import { Outlet, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useShell } from "@/app/layout/useShell";
import TenantHeader from "@/features/tenants/TenantHeader.vue";
import { provideCanWrite } from "@/shared/lib/access";
import { useApps, useMe } from "./api";

/**
 * The deeper views of one environment of an app: customers, subscriptions, credits, packages, pricing, invoices, payments, payment links, accounting,
 * reports, webhooks and e-mail. They are the console's own pages where those speak the tenant API (reused as they are) and pages of the product where the
 * console's version needs operator-only routes (invoices, payments). All of them speak the tenant API with the person's session token and the tenant as
 * X-Quotify-Tenant, and the gateway lets that through only for a tenant of the person's accounts; a viewer's session may only read. The sidebar is the
 * navigation; this is the page head.
 */
const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const shell = useShell();
const apps = useApps();
const me = useMe();
const tenant = computed(() => shell.located.value?.app.tenants?.find((x) => x.id === id.value));
const title = computed(() => (shell.current.value ? t(`shell.items.${shell.current.value.label}`) : "…"));

/** The person's role in the account that owns this tenant; unknown until the app list and the profile have arrived. */
const role = computed(() => me.data.value?.memberships?.find((m) => m.account_id === shell.located.value?.app.account_id)?.role);
const canWrite = computed(() => role.value !== undefined && role.value !== "viewer");
provideCanWrite(canWrite);
</script>

<template>
  <section class="flex flex-col">
    <TenantHeader :title="title" :name="shell.located.value?.app.name ?? (apps.isPending.value ? '…' : '')" :tenant="tenant" />
    <p v-if="role === 'viewer'" class="bg-muted text-muted-foreground mb-6 rounded-2xl px-4 py-3 text-sm" role="status" data-testid="read-only-note">{{ t("product.links.readOnly") }}</p>
    <Outlet />
  </section>
</template>
