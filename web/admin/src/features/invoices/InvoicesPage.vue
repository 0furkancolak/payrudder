<script setup lang="ts">
import { useParams } from "@tanstack/vue-router";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { Card } from "@/shared/ui/card";
import InvoiceTable from "./InvoiceTable.vue";

/** The invoices of a tenant, for the person and for the operator. */
const { t } = useI18n();
const params = useParams({ strict: false });
const id = computed(() => String(params.value.id ?? ""));
</script>

<template>
  <Card class="p-3">
    <h2 class="sr-only">{{ t("tenant.invoices") }}</h2>
    <InvoiceTable :tenant-id="id" />
  </Card>
</template>
