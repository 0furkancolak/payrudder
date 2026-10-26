<script setup lang="ts">
import { Link, useParams } from "@tanstack/vue-router";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { formatMinor } from "@/shared/lib/money";
import { ErrorText } from "@/shared/ui/error-text";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { matchesSearch } from "@/shared/lib/paging";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill, toneOf } from "@/shared/ui/status-pill";
import { useInvoices } from "./billingApi";

/** The environment's invoices: number, customer, period, total (in its own currency), status. A row opens the invoice. Reads /v1/invoices only. */
const { t, locale } = useI18n();
const fmt = useFormat();
const params = useParams({ strict: false });
const tenantId = computed(() => String(params.value.id ?? ""));
const status = ref("");
const invoices = useInvoices(tenantId, status);

/** The API hands out invoices a page at a time; `at` is the page shown, and going past the last one loaded fetches the next. */
const at = ref(0);
const search = ref("");
watch(status, () => (at.value = 0));
const loaded = computed(() => invoices.data.value?.pages ?? []);
const rows = computed(() => (loaded.value[at.value]?.invoices ?? []).filter((i) => matchesSearch([i.number, i.customer_external_id, i.period_id], search.value)));
const hasNext = computed(() => at.value + 1 < loaded.value.length || !!invoices.hasNextPage.value);
async function next() {
  if (at.value + 1 >= loaded.value.length) await invoices.fetchNextPage();
  at.value = Math.min(at.value + 1, Math.max(0, loaded.value.length - 1));
}
const statuses = ["draft", "open", "paid", "uncollectible", "void"] as const;
const day = (v?: string) => fmt.date(v, "day");
</script>

<template>
  <section class="surface flex flex-col gap-4 p-6" :aria-label="t('product.billing.invoices.title')">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold tracking-tight">{{ t("product.billing.invoices.title") }}</h2>
        <p class="text-muted-foreground text-sm">{{ t("product.billing.invoices.intro") }}</p>
      </div>
    </div>

    <DataToolbar v-model:search="search" :active="search !== '' || status !== ''" @clear="search = ''; status = ''">
      <NativeSelect id="inv-status" v-model="status" class="w-44" :aria-label="t('product.billing.invoices.status')">
        <option value="">{{ t("product.billing.invoices.all") }}</option>
        <option v-for="s in statuses" :key="s" :value="s">{{ t(`product.invoiceStatus.${s}`) }}</option>
      </NativeSelect>
    </DataToolbar>

    <ErrorText :error="invoices.error.value" class="bg-destructive-soft rounded-2xl px-4 py-3" />
    <Skeleton v-if="invoices.isPending.value" class="h-40 rounded-2xl" />
    <p v-else-if="rows.length === 0 && !invoices.error.value" class="text-muted-foreground py-10 text-center text-sm">{{ t(search !== "" || status !== "" ? "common.noMatches" : "product.billing.invoices.empty") }}</p>
    <div v-else class="relative -mx-2 overflow-x-auto px-2">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-muted-foreground text-xs">
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoices.number") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoices.customer") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoices.period") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoices.created") }}</th>
            <th scope="col" class="py-2 pe-4 text-start font-medium">{{ t("product.billing.invoices.status") }}</th>
            <th scope="col" class="py-2 text-end font-medium">{{ t("product.billing.invoices.total") }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="inv in rows" :key="inv.id" class="border-border/70 border-t" :data-testid="`invoice-${inv.number || inv.id}`">
            <td class="py-3 pe-4 font-medium whitespace-nowrap">
              <Link to="/app/t/$id/invoices/$invoiceId" :params="{ id: tenantId, invoiceId: inv.id ?? '' }" class="hover:underline">{{ inv.number || t("product.billing.invoices.draft") }}</Link>
            </td>
            <td class="py-3 pe-4">{{ inv.customer_external_id }}</td>
            <td class="text-muted-foreground py-3 pe-4 whitespace-nowrap">{{ inv.period_id }}</td>
            <td class="text-muted-foreground py-3 pe-4 whitespace-nowrap">{{ day(inv.create_time) }}</td>
            <td class="py-3 pe-4"><StatusPill :tone="toneOf(inv.status)">{{ t(`product.invoiceStatus.${inv.status}`) }}</StatusPill></td>
            <td class="py-3 text-end font-medium whitespace-nowrap tabular">{{ formatMinor(inv.total_minor, inv.currency, locale) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <DataPagination :page="at + 1" :has-prev="at > 0" :has-next="hasNext" @prev="at--" @next="next" />
  </section>
</template>
