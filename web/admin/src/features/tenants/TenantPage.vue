<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { toApiError } from "@/shared/api/client";
import { InvoiceTable } from "@/features/invoices";
import { enumWord, label } from "@/shared/lib/labels";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { PageHeader } from "@/shared/ui/page-header";
import { useTenant } from "./api";
import JobLookup from "./JobLookup.vue";

const { t, te } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
const tenant = useTenant(id);
</script>

<template>
  <PageHeader :title="tenant.data.value?.name ?? t('tenant.fallbackTitle')" />
  <p v-if="tenant.error.value" class="text-destructive mb-3" role="alert">{{ toApiError(tenant.error.value).message }}</p>
  <div v-if="tenant.data.value" class="text-muted-foreground -mt-3 mb-6 flex flex-wrap items-center gap-2 px-1 text-sm">
    <Badge variant="lime">{{ label(t, te, "environment", enumWord(tenant.data.value.environment, "ENVIRONMENT_")) }}</Badge>
    <span>{{ tenant.data.value.currency }} · {{ tenant.data.value.timezone }} · {{ t("tenant.grace", { value: tenant.data.value.grace_period ?? t("common.unknown") }) }}</span>
    <span class="mono text-xs">{{ tenant.data.value.id }}</span>
  </div>

  <Card class="mb-6 p-3">
    <h2 class="px-3 pt-2 text-lg font-semibold">{{ t("tenant.invoices") }}</h2>
    <InvoiceTable :tenant-id="id" />
  </Card>

  <Card>
    <div>
      <h2 class="text-lg font-semibold">{{ t("tenant.jobs.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("tenant.jobs.hint") }}</p>
    </div>
    <JobLookup :tenant-id="id" />
  </Card>
</template>
