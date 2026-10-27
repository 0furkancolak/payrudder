<script setup lang="ts">
import { Gift } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useFormat } from "@/shared/lib/format";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCreditBalance } from "./api";
import GrantCreditsSheet from "./GrantCreditsSheet.vue";
import { formatCredits } from "./lib";

/** One customer's credits: what can be spent now, the lots it comes from, and the sheet to give them free credits (GrantCreditsSheet). */
const props = defineProps<{ tenantId: string }>();
const customer = defineModel<string>("customer", { required: true });
const { t, locale } = useI18n();
const fmt = useFormat();

const balance = useCreditBalance(() => props.tenantId, customer);
const credits = (v: string | number | undefined) => formatCredits(v, locale.value);

const open = ref(false);
type Lot = NonNullable<NonNullable<typeof balance.data.value>["lots"]>[number];
const lots = useTableState<Lot>(() => balance.data.value?.lots ?? [], {
  searchFields: (l) => [l.source ? t(`billing.credits.source.${l.source}`) : ""],
  filters: { source: (l, v) => l.source === v },
});
const sourceFilter = computed({ get: () => lots.filters.value.source ?? [], set: (v: string[]) => (lots.filters.value.source = v) });
// The sources a lot can come from, as far as this customer's lots show.
const sources = computed(() => [{ value: "", label: t("common.all") }, ...[...new Set((balance.data.value?.lots ?? []).map((l) => l.source ?? "").filter(Boolean))].map((s) => ({ value: s, label: t(`billing.credits.source.${s}`) }))]);
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="grid gap-3 px-3 pt-2 pb-3">
      <h2 class="text-base font-semibold">{{ t("billing.credits.customer.title") }}</h2>
      <CustomerSelect v-model="customer" :tenant-id="tenantId" named :label="t('billing.credits.customer.pick')" :placeholder="t('billing.credits.customer.pick')" />
    </div>

    <template v-if="customer">
      <ErrorText :error="balance.error.value" class="px-3" />
      <div v-if="balance.data.value" class="flex flex-wrap items-baseline gap-x-4 gap-y-1 px-3 pb-3" role="status" :aria-label="t('billing.credits.customer.balance')">
        <span class="text-3xl font-semibold tabular-nums">{{ credits(balance.data.value.balance) }}</span>
        <span class="text-muted-foreground text-sm">{{ t("billing.credits.customer.balance") }}</span>
        <span v-if="balance.data.value.next_expire_time" class="text-muted-foreground text-sm">
          {{ t("billing.credits.customer.nextExpiry", { when: fmt.date(balance.data.value.next_expire_time, "dateTime") }) }}
        </span>
      </div>

      <DataToolbar v-model:search="lots.search.value" :active="lots.active.value" @clear="lots.clear()">
        <FilterSelect v-model="sourceFilter" multiple :label="t('billing.credits.lots.source')" :options="sources" />
        <template #actions>
          <Button type="button" data-testid="credits-grant" @click="open = true"><Gift class="size-4" aria-hidden="true" />{{ t("billing.credits.grant.free") }}</Button>
        </template>
      </DataToolbar>
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead>{{ t("billing.credits.lots.source") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.remaining") }}</TableHead>
            <TableHead class="text-end">{{ t("billing.credits.lots.granted") }}</TableHead>
            <TableHead>{{ t("billing.credits.lots.expires") }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="l in lots.items.value" :key="l.id">
            <TableCell><StatusPill :tone="l.source === 'purchase' ? 'success' : 'neutral'">{{ t(`billing.credits.source.${l.source}`) }}</StatusPill></TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.remaining) }}</TableCell>
            <TableCell class="text-end tabular-nums">{{ credits(l.granted) }}</TableCell>
            <TableCell>{{ l.expire_time ? fmt.date(l.expire_time, "dateTime") : t("billing.credits.lots.never") }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p v-if="lots.total.value === 0 && lots.active.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
      <DataPagination v-model:page="lots.page.value" v-model:size="lots.size.value" :pages="lots.pages.value" :total="lots.total.value" />
    </template>

    <GrantCreditsSheet v-model:open="open" :tenant-id="tenantId" :customer="customer" />
  </Card>
</template>
