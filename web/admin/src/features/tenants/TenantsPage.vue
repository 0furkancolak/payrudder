<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { enumWord, label } from "@/shared/lib/labels";
import { toApiError } from "@/shared/api/client";
import { useTenants } from "./api";

const { t, te } = useI18n();
const tenants = useTenants();
</script>

<template>
  <h1>{{ t("tenants.title") }}</h1>
  <p v-if="tenants.isPending.value" class="muted">{{ t("common.loading") }}</p>
  <p v-if="tenants.error.value" class="error">{{ toApiError(tenants.error.value).message }}</p>
  <table>
    <thead>
      <tr>
        <th>{{ t("tenants.columns.name") }}</th>
        <th>{{ t("tenants.columns.environment") }}</th>
        <th>{{ t("tenants.columns.tier") }}</th>
        <th>{{ t("tenants.columns.currency") }}</th>
        <th>{{ t("tenants.columns.timezone") }}</th>
        <th>{{ t("tenants.columns.id") }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="tenant in tenants.data.value?.tenants ?? []" :key="tenant.id">
        <td>
          <Link to="/tenants/$id" :params="{ id: tenant.id ?? '' }">{{ tenant.name }}</Link>
        </td>
        <td><span class="badge">{{ label(t, te, "environment", enumWord(tenant.environment, "ENVIRONMENT_")) }}</span></td>
        <td>{{ label(t, te, "tier", enumWord(tenant.tier, "TENANT_TIER_")) }}</td>
        <td>{{ tenant.currency }}</td>
        <td>{{ tenant.timezone }}</td>
        <td class="mono">{{ tenant.id }}</td>
      </tr>
    </tbody>
  </table>
  <p v-if="!tenants.isPending.value && (tenants.data.value?.tenants ?? []).length === 0 && !tenants.error.value" class="muted">
    {{ t("tenants.empty") }}
  </p>
</template>
