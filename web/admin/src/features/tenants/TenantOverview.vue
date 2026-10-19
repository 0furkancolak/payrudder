<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { InvoiceTable } from "@/features/invoices";
import { Card } from "@/shared/ui/card";
import JobLookup from "./JobLookup.vue";

const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
</script>

<template>
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
