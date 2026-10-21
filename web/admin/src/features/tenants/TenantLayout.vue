<script setup lang="ts">
import { Link, Outlet, useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { enumWord, label } from "@/shared/lib/labels";
import { Badge } from "@/shared/ui/badge";
import { PageHeader } from "@/shared/ui/page-header";
import { useTenant } from "./api";

const { t, te } = useI18n();
const errorText = useErrorText();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(id);

const tab = "text-muted-foreground hover:text-foreground border-b-2 border-transparent px-3 py-2 text-sm whitespace-nowrap transition-colors";
const tabActive = "!text-foreground border-foreground font-medium";
const tabs = [
  { to: "/tenants/$id", key: "overview", exact: true },
  { to: "/tenants/$id/customers", key: "customers", exact: false },
  { to: "/tenants/$id/packages", key: "packages", exact: false },
  { to: "/tenants/$id/subscriptions", key: "subscriptions", exact: false },
  { to: "/tenants/$id/pricing", key: "pricing", exact: false },
  { to: "/tenants/$id/payments", key: "payments", exact: false },
  { to: "/tenants/$id/providers", key: "providers", exact: false },
  { to: "/tenants/$id/reports", key: "reports", exact: false },
  { to: "/tenants/$id/webhooks", key: "webhooks", exact: false },
  { to: "/tenants/$id/mail", key: "mail", exact: false },
  { to: "/tenants/$id/apps", key: "apps", exact: false },
  { to: "/tenants/$id/address", key: "address", exact: false },
] as const;
</script>

<template>
  <PageHeader :title="tenant.data.value?.name ?? t('tenant.fallbackTitle')" />
  <p v-if="tenant.error.value" class="text-destructive mb-3" role="alert">{{ errorText(tenant.error.value) }}</p>
  <div v-if="tenant.data.value" class="text-muted-foreground -mt-3 mb-4 flex flex-wrap items-center gap-2 px-1 text-sm">
    <Badge variant="secondary">{{ label(t, te, "environment", enumWord(tenant.data.value.environment, "ENVIRONMENT_")) }}</Badge>
    <span>{{ tenant.data.value.currency }} · {{ tenant.data.value.timezone }} · {{ t("tenant.grace", { value: tenant.data.value.grace_period ?? t("common.unknown") }) }}</span>
    <span class="mono text-xs">{{ tenant.data.value.id }}</span>
  </div>

  <nav class="mb-6 flex gap-1 overflow-x-auto border-b" :aria-label="t('billing.tabs.label')">
    <Link v-for="item in tabs" :key="item.key" :to="item.to" :params="{ id }" :class="tab" :active-props="{ class: tabActive }" :active-options="{ exact: item.exact }">
      {{ t(`billing.tabs.${item.key}`) }}
    </Link>
  </nav>

  <Outlet />
</template>
