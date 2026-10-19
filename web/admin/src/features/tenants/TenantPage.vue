<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { InvoiceTable } from "@/features/invoices";
import { enumWord, label } from "@/shared/lib/labels";
import { useTenant } from "./api";
import JobLookup from "./JobLookup.vue";

const { t, te } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(id);
</script>

<template>
  <h1>{{ tenant.data.value?.name ?? t("tenant.fallbackTitle") }}</h1>
  <p v-if="tenant.error.value" class="error">{{ toApiError(tenant.error.value).message }}</p>
  <p v-if="tenant.data.value" class="muted">
    <span class="badge">{{ label(t, te, "environment", enumWord(tenant.data.value.environment, "ENVIRONMENT_")) }}</span>
    {{ tenant.data.value.currency }} · {{ tenant.data.value.timezone }} · {{ t("tenant.grace", { value: tenant.data.value.grace_period ?? t("common.unknown") }) }} ·
    <span class="mono">{{ tenant.data.value.id }}</span>
  </p>

  <h2>{{ t("tenant.invoices") }}</h2>
  <InvoiceTable :tenant-id="id" />

  <h2>{{ t("tenant.jobs.title") }}</h2>
  <p class="muted">{{ t("tenant.jobs.hint") }}</p>
  <JobLookup :tenant-id="id" />
</template>
