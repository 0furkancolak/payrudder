<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { ArrowLeft } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import InvoiceDetail from "./InvoiceDetail.vue";

/** One invoice on a page of its own (the address a link or a bookmark has); the invoice list opens the same content in a sheet. */
const { t } = useI18n();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const invoiceId = computed(() => String(params.value.invoiceId ?? ""));
</script>

<template>
  <section class="flex flex-col gap-6">
    <Link to="/app/t/$id/invoices" :params="{ id: tenantId }" class="text-muted-foreground hover:text-foreground inline-flex w-fit items-center gap-1 px-1 text-sm">
      <ArrowLeft class="size-4 rtl:rotate-180" />{{ t("product.billing.invoice.back") }}
    </Link>
    <InvoiceDetail :tenant-id="tenantId" :invoice-id="invoiceId" />
  </section>
</template>
