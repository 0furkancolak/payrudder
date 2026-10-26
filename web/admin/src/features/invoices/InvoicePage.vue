<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { ArrowLeft } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { PageHeader } from "@/shared/ui/page-header";
import { useInvoice } from "./api";
import InvoiceDetail from "./InvoiceDetail.vue";

/** One invoice on a page of its own (a deep link); the invoices list opens the same content in a sheet. */
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));
const invoice = useInvoice(tenantId, invoiceId);
</script>

<template>
  <Link to="/tenants/$id" :params="{ id: tenantId }" class="text-muted-foreground hover:text-foreground mb-3 inline-flex items-center gap-1 px-1 text-sm">
    <ArrowLeft class="rtl-flip size-4" />{{ t("invoice.back") }}
  </Link>
  <PageHeader :title="invoice.data.value?.number || t('invoice.fallbackTitle')" />
  <InvoiceDetail :tenant-id="tenantId" :invoice-id="invoiceId" />
</template>
