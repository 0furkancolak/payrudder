<script setup lang="ts">
import { Coins } from "@lucide/vue";
import { TabsContent } from "reka-ui";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import CustomerSelect from "@/features/customers/CustomerSelect.vue";
import { useFormat } from "@/shared/lib/format";
import { useTabParam } from "@/shared/lib/useSearchValue";
import { useTableState } from "@/shared/lib/useTableState";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs";
import { useCreditBalance, useCreditLedger } from "./api";
import GrantCreditsSheet from "./GrantCreditsSheet.vue";
import LedgerCard from "./LedgerCard.vue";
import { formatCredits } from "./lib";

/**
 * One customer's credits: the balance on top (what can be spent now, when some of it runs out, and the sheet to give free credits: GrantCreditsSheet),
 * and below it the grants the balance comes from and the ledger of everything that happened to it, in a tab each.
 */
const props = defineProps<{ tenantId: string }>();
const customer = defineModel<string>("customer", { required: true });
const { t, locale } = useI18n();
const fmt = useFormat();

const balance = useCreditBalance(() => props.tenantId, customer);
const credits = (v: string | number | undefined) => formatCredits(v, locale.value);

// A lot only knows its kind (promotion, manual, purchase); why it was granted ("Welcome gift", "Goodwill") is the reason of the ledger entry that made it.
const ledger = useCreditLedger(() => props.tenantId, customer);
const reasonOfLot = computed(() => {
  const m = new Map<string, string>();
  for (const e of ledger.data.value?.entries ?? []) if (e.kind === "grant" && e.reason && !m.has(e.lot_id)) m.set(e.lot_id, e.reason);
  return m;
});

const open = ref(false);
const view = useTabParam("view", ["lots", "ledger"] as const, "lots");
type Lot = NonNullable<NonNullable<typeof balance.data.value>["lots"]>[number];
const lots = useTableState<Lot>(() => balance.data.value?.lots ?? [], {
  searchFields: (l) => [l.source ? t(`billing.credits.source.${l.source}`) : ""],
  filters: { source: (l, v) => l.source === v },
});
const sourceFilter = computed({ get: () => lots.filters.value.source ?? [], set: (v: string[]) => (lots.filters.value.source = v) });
// The sources a lot can come from, as far as this customer's lots show.
const sources = computed(() => [{ value: "", label: t("common.all") }, ...[...new Set((balance.data.value?.lots ?? []).map((l) => l.source ?? "").filter(Boolean))].map((s) => ({ value: s, label: t(`billing.credits.source.${s}`) }))]);
const TAB_CLASS =
  "text-muted-foreground hover:text-foreground data-[state=active]:text-foreground data-[state=active]:border-primary h-9 flex-none rounded-none border-0 border-b-2 border-transparent bg-transparent px-0.5 font-medium data-[state=active]:bg-transparent data-[state=active]:shadow-none";
</script>

<template>
  <Card class="mb-6 gap-0 p-3" data-testid="credits-balance">
    <div class="grid gap-3 px-3 pt-2 pb-3">
      <h2 class="text-base font-semibold">{{ t("billing.credits.customer.title") }}</h2>
      <CustomerSelect v-model="customer" :tenant-id="tenantId" named :label="t('billing.credits.customer.pick')" :placeholder="t('billing.credits.customer.pick')" />
    </div>

    <p v-if="!customer" class="text-muted-foreground px-3 pb-3 text-sm">{{ t("billing.credits.customer.empty") }}</p>
    <template v-else>
      <ErrorText :error="balance.error.value" class="px-3 pb-2" />
      <div class="bg-muted/50 mx-3 mb-4 flex flex-wrap items-center justify-between gap-x-6 gap-y-3 rounded-2xl p-4">
        <Skeleton v-if="balance.isPending.value" class="h-12 w-48" />
        <div v-else-if="balance.data.value" class="flex min-w-0 flex-col gap-0.5" role="status" :aria-label="t('billing.credits.customer.balance')">
          <span class="text-3xl font-semibold tabular-nums" data-testid="credits-balance-value">{{ credits(balance.data.value.balance) }}</span>
          <span class="text-muted-foreground text-sm">{{ t("billing.credits.customer.balance") }}</span>
          <span v-if="balance.data.value.next_expire_time" class="text-muted-foreground text-sm">
            {{ t("billing.credits.customer.nextExpiry", { when: fmt.date(balance.data.value.next_expire_time, "dateTime") }) }}
          </span>
        </div>
        <Button type="button" data-testid="credits-grant" @click="open = true"><Coins class="size-4" aria-hidden="true" />{{ t("billing.credits.grant.free") }}</Button>
      </div>

      <Tabs v-model="view" class="min-w-0 gap-3">
        <TabsList class="border-border/70 mx-3 w-auto max-w-full flex-nowrap justify-start gap-4 overflow-x-auto border-b" :aria-label="t('billing.credits.customer.title')">
          <TabsTrigger value="lots" :class="TAB_CLASS" data-testid="credits-tab-lots">{{ t("billing.credits.tabs.lots") }}</TabsTrigger>
          <TabsTrigger value="ledger" :class="TAB_CLASS" data-testid="credits-tab-ledger">{{ t("billing.credits.ledger.title") }}</TabsTrigger>
        </TabsList>

        <TabsContent value="lots" class="min-w-0 outline-none">
          <DataToolbar v-model:search="lots.search.value" :active="lots.active.value" @clear="lots.clear()">
            <FilterSelect v-model="sourceFilter" multiple :label="t('billing.credits.lots.source')" :options="sources" />
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
                <TableCell><StatusPill :tone="l.source === 'purchase' ? 'success' : 'neutral'">{{ t(`billing.credits.source.${l.source}`) }}</StatusPill><span v-if="reasonOfLot.get(l.id)" class="text-muted-foreground ms-2 text-xs" data-testid="lot-reason">{{ reasonOfLot.get(l.id) }}</span></TableCell>
                <TableCell class="text-end tabular-nums">{{ credits(l.remaining) }}</TableCell>
                <TableCell class="text-end tabular-nums">{{ credits(l.granted) }}</TableCell>
                <TableCell>{{ l.expire_time ? fmt.date(l.expire_time, "dateTime") : t("billing.credits.lots.never") }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
          <TableEmpty v-if="lots.total.value === 0 && lots.active.value">{{ t("common.noMatches") }}</TableEmpty>
          <DataPagination v-model:page="lots.page.value" v-model:size="lots.size.value" :pages="lots.pages.value" :total="lots.total.value" />
        </TabsContent>

        <TabsContent value="ledger" class="min-w-0 outline-none">
          <LedgerCard :tenant-id="tenantId" :customer="customer" bare />
        </TabsContent>
      </Tabs>
    </template>

    <GrantCreditsSheet v-model:open="open" :tenant-id="tenantId" :customer="customer" />
  </Card>
</template>
