<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { DataPagination } from "@/shared/ui/data-table";
import { usePager } from "@/features/affiliates/paging";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { ErrorState } from "@/shared/ui/error-state";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { usePayouts } from "./api";
import { payoutTone } from "./logic";

/** What the seller paid the partner. A payout of a sandbox environment is fake: the badge says nothing was paid. */
const props = defineProps<{ id: string }>();
const { t } = useI18n();
const fmt = useFormat();
const pager = usePager();
const list = usePayouts(() => props.id, pager.token, pager.size);
</script>

<template>
  <Card class="p-3" data-testid="payouts">
    <ErrorState :error="list.error.value" :retrying="list.isFetching.value" compact @retry="list.refetch()" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-24" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground m-0 px-3 py-4 text-sm">{{ t("partner.payouts.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.payouts.created_at") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.payouts.total") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead>{{ t("affiliates.payouts.reference") }}</TableHead>
          <TableHead>{{ t("partner.payouts.paidOn") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="p in list.data.value.items" :key="p.id" :data-payout="p.id">
          <TableCell class="whitespace-nowrap">{{ fmt.date(p.create_time, "short") }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.amount(p.total_minor, p.currency) }}</TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <StatusPill :tone="payoutTone(p.status)">{{ t(`affiliates.status.payout.${p.status}`) }}</StatusPill>
              <StatusPill v-if="p.fake" tone="warning" data-testid="fake-badge">{{ t("affiliates.payouts.fake") }}</StatusPill>
            </div>
          </TableCell>
          <TableCell dir="auto" class="max-w-48 truncate">{{ p.reference || "—" }}</TableCell>
          <TableCell class="whitespace-nowrap">{{ fmt.date(p.pay_time, "short", "—") }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
</template>
