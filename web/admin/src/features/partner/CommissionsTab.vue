<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { DataPagination } from "@/shared/ui/data-table";
import { usePager } from "@/features/affiliates/paging";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import FilterBar from "@/features/affiliates/FilterBar.vue";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCommissions } from "./api";
import { commissionTone, isReversal } from "./logic";

/** The partner's commissions. A reversal row (negative) nets a commission that was already paid; it is drawn apart. No invoice or customer id is ever shown. */
const props = defineProps<{ id: string }>();
const { t } = useI18n();
const fmt = useFormat();
const status = ref("");
const pager = usePager();
watch(status, () => pager.reset());
const statusOptions = computed(() => [{ value: "", label: t("affiliates.commissions.allStatuses") }, ...["pending", "approved", "paid", "reversed"].map((x) => ({ value: x, label: t(`affiliates.status.commission.${x}`) }))]);
const list = useCommissions(() => props.id, status, pager.token, pager.size);
</script>

<template>
  <Card class="p-3" data-testid="commissions">
    <FilterBar :active="status !== ''" class="pt-2" @clear="status = ''">
      <FilterSelect v-model="status" :label="t('affiliates.common.status')" :options="statusOptions" />
    </FilterBar>
    <p class="text-muted-foreground m-0 px-3 pb-2 text-xs">{{ t("partner.commissions.hint") }}</p>
    <ErrorText :error="list.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-24" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground m-0 px-3 py-4 text-sm">{{ t("partner.commissions.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.commissions.earned") }}</TableHead>
          <TableHead>{{ t("partner.referrals.customer") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.commissions.amount") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="c in list.data.value.items" :key="c.id" :data-kind="c.kind" :class="isReversal(c) ? 'bg-destructive-soft/40' : ''">
          <TableCell class="whitespace-nowrap">{{ fmt.date(c.earn_time, "short") }}</TableCell>
          <TableCell dir="ltr" class="text-start">{{ c.customer_email_masked }}</TableCell>
          <TableCell class="text-end tabular-nums" :class="isReversal(c) ? 'text-destructive font-medium' : ''">
            {{ fmt.amount(c.amount_minor, c.currency) }}
            <div v-if="!isReversal(c) && c.reversed_minor && c.reversed_minor !== '0'" class="text-destructive text-xs">{{ t("affiliates.commissions.reversedOf", { amount: fmt.amount(c.reversed_minor, c.currency) }) }}</div>
            <div v-if="c.status === 'pending' && c.approve_after_time" class="text-muted-foreground text-xs">{{ t("partner.commissions.payableFrom", { date: fmt.date(c.approve_after_time, "day") }) }}</div>
          </TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <StatusPill v-if="isReversal(c)" tone="danger">{{ t("affiliates.commissions.kinds.reversal") }}</StatusPill>
              <StatusPill :tone="commissionTone(c.status)">{{ t(`affiliates.status.commission.${c.status}`) }}</StatusPill>
            </div>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
</template>
