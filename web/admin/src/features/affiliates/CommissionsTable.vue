<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Input } from "@/shared/ui/input";
import { NativeSelect } from "@/shared/ui/native-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliateOptions, useCommissions, type CommissionFilter } from "./api";
import { commissionTone, dayEnd, dayStart, isReversal, type Commission } from "./logic";
import Pager from "./Pager.vue";
import { usePager } from "./paging";
import ReverseDialog from "./ReverseDialog.vue";

/**
 * Commissions and the reversal rows that net against them. With `payoutId` it is the commissions of one payout (no filters, no payout column);
 * without, it is the whole list with its filters.
 */
const props = defineProps<{ tenantId: string; payoutId?: string }>();
defineEmits<{ payout: [id: string] }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const affiliate = ref("");
const status = ref("");
const kind = ref("");
const fromDay = ref("");
const toDay = ref("");
const filter = computed<CommissionFilter>(() => ({
  affiliate_id: affiliate.value,
  status: status.value,
  kind: kind.value,
  payout_id: props.payoutId ?? "",
  from: dayStart(fromDay.value) ?? "",
  to: dayEnd(toDay.value) ?? "",
}));
const pager = usePager();
watch(filter, () => pager.reset());

const options = useAffiliateOptions(tenantId);
const list = useCommissions(tenantId, filter, pager.token);

const reversing = ref<Commission | null>(null);
const reverseOpen = ref(false);
function reverse(c: Commission) {
  reversing.value = c;
  reverseOpen.value = true;
}
/** Only a commission that still has something left can be taken back; a reversal row is the taking back itself. */
const canReverse = (c: Commission) => !isReversal(c) && c.status !== "reversed" && !!c.invoice_id;
</script>

<template>
  <Card class="p-3" data-testid="commissions">
    <div v-if="!payoutId" class="flex flex-wrap items-center gap-2 px-3 pt-2">
      <NativeSelect v-model="affiliate" class="w-44" :aria-label="t('affiliates.commissions.partner')">
        <option value="">{{ t("affiliates.commissions.allPartners") }}</option>
        <option v-for="a in options.data.value ?? []" :key="a.id" :value="a.id">{{ a.code }}</option>
      </NativeSelect>
      <NativeSelect v-model="status" class="w-40" :aria-label="t('affiliates.common.status')">
        <option value="">{{ t("affiliates.commissions.allStatuses") }}</option>
        <option v-for="s in ['pending', 'approved', 'paid', 'reversed']" :key="s" :value="s">{{ t(`affiliates.status.commission.${s}`) }}</option>
      </NativeSelect>
      <NativeSelect v-model="kind" class="w-40" :aria-label="t('affiliates.commissions.kind')">
        <option value="">{{ t("affiliates.commissions.allKinds") }}</option>
        <option value="commission">{{ t("affiliates.commissions.kinds.commission") }}</option>
        <option value="reversal">{{ t("affiliates.commissions.kinds.reversal") }}</option>
      </NativeSelect>
      <Input v-model="fromDay" type="date" class="w-40" :aria-label="t('billing.accounting.period.from')" />
      <Input v-model="toDay" type="date" class="w-40" :aria-label="t('billing.accounting.period.to')" />
    </div>
    <ErrorText :error="list.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-32" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground px-3 py-4 text-sm">{{ t("affiliates.commissions.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.commissions.earned") }}</TableHead>
          <TableHead>{{ t("affiliates.commissions.partner") }}</TableHead>
          <TableHead>{{ t("affiliates.commissions.invoice") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.commissions.base") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.commissions.amount") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead v-if="!payoutId">{{ t("affiliates.commissions.payout") }}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow
          v-for="c in list.data.value.items"
          :key="c.id"
          :data-kind="c.kind"
          :class="isReversal(c) ? 'bg-destructive-soft/40' : ''"
        >
          <TableCell class="whitespace-nowrap">{{ fmt.date(c.earn_time, "short") }}</TableCell>
          <TableCell>
            <span class="mono font-medium">{{ c.affiliate_code }}</span>
            <div class="text-muted-foreground text-xs" dir="auto">{{ c.customer_external_id }}</div>
          </TableCell>
          <TableCell>{{ c.invoice_number || c.invoice_id }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.amount(c.net_minor, c.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums" :class="isReversal(c) ? 'text-destructive font-medium' : ''">
            {{ fmt.amount(c.amount_minor, c.currency) }}
            <div v-if="!isReversal(c) && c.reversed_minor && c.reversed_minor !== '0'" class="text-destructive text-xs">{{ t("affiliates.commissions.reversedOf", { amount: fmt.amount(c.reversed_minor, c.currency) }) }}</div>
          </TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <StatusPill v-if="isReversal(c)" tone="danger">{{ t("affiliates.commissions.kinds.reversal") }}</StatusPill>
              <StatusPill :tone="commissionTone(c.status)">{{ t(`affiliates.status.commission.${c.status}`) }}</StatusPill>
            </div>
          </TableCell>
          <TableCell v-if="!payoutId">
            <Button v-if="c.payout_id" type="button" variant="link" size="sm" class="mono h-auto p-0" :aria-label="t('affiliates.commissions.openPayout')" @click="$emit('payout', c.payout_id)">{{ c.payout_id.slice(0, 8) }}</Button>
            <span v-else class="text-muted-foreground">—</span>
          </TableCell>
          <TableCell class="text-end">
            <Button v-if="canReverse(c)" type="button" variant="outline" size="sm" :disabled="!canWrite" @click="reverse(c)">{{ t("affiliates.commissions.reverse") }}</Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <Pager v-if="list.data.value" :has-prev="pager.hasPrev.value" :next="list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
  <ReverseDialog v-model:open="reverseOpen" :tenant-id="tenantId" :commission="reversing" />
</template>
