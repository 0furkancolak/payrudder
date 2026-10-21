<script setup lang="ts">
import { useNavigate, useParams, useSearch } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import DashboardView from "@/features/dashboard/DashboardView.vue";
import { parseQuery, toSearch, type DashQuery } from "@/features/dashboard/lib";
import { InvoiceTable } from "@/features/invoices";
import { Card } from "@/shared/ui/card";
import JobLookup from "./JobLookup.vue";

/** A tenant for the operator: the dashboard of the tenant, its newest invoices and the lookup of replay and re-rating jobs. */
const { t } = useI18n();
const params = useParams({ strict: false });
const search = useSearch({ strict: false });
const navigate = useNavigate();
const id = computed(() => String(params.value.id ?? ""));
const query = computed(() => parseQuery(search.value as Record<string, unknown>));
const source = computed(() => (id.value ? { kind: "tenant" as const, tenantId: id.value } : null));

function setQuery(q: DashQuery) {
  void navigate({ to: "/tenants/$id", params: { id: id.value }, search: toSearch(q) as never, replace: true });
}
</script>

<template>
  <DashboardView :source="source" :base="`/tenants/${id}`" :query="query" class="mb-6" @update:query="setQuery" />

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
