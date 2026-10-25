<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import Pager from "@/features/affiliates/Pager.vue";
import { usePager } from "@/features/affiliates/paging";
import { useFormat } from "@/shared/lib/format";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { NativeSelect } from "@/shared/ui/native-select";
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
const list = useCommissions(() => props.id, status, pager.token);
</script>

<template>
  <Card class="p-3" data-testid="commissions">
    <div class="flex flex-wrap items-center gap-2 px-3 pt-2">
      <NativeSelect v-model="status" class="w-44" :aria-label="t('affiliates.common.status')">
        <option value="">{{ t("affiliates.commissions.allStatuses") }}</option>
        <option v-for="s in ['pending', 'approved', 'paid', 'reversed']" :key="s" :value="s">{{ t(`affiliates.status.commission.${s}`) }}</option>
      </NativeSelect>
    </div>
    <p class="text-muted-foreground m-0 px-3 text-xs">{{ t("partner.commissions.hint") }}</p>
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
    <Pager v-if="list.data.value" :has-prev="pager.hasPrev.value" :next="list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
</template>
