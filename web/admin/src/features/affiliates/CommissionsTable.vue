<script setup lang="ts">
import { rangeChanged, thisMonth } from "@/shared/lib/dateRange";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Undo2 } from "@lucide/vue";
import { DateRangePicker, type DateRange } from "@/shared/ui/date-range";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import FilterBar from "./FilterBar.vue";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliateOptions, useCommissions, type CommissionFilter } from "./api";
import { commissionTone, dayEnd, dayStart, isReversal, type Commission } from "./logic";
import { DataPagination } from "@/shared/ui/data-table";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { useSheetTarget } from "@/shared/lib/useSheetTarget";
import { usePager } from "./paging";
import ReverseDialog from "./ReverseDialog.vue";

/**
 * Commissions and the reversal rows that net against them. With `payoutId` it is the commissions of one payout (no filters, no payout column);
 * without, it is the whole list with its filters.
 */
const props = defineProps<{ tenantId: string; payoutId?: string }>();
const emit = defineEmits<{ payout: [id: string] }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const affiliate = ref("");
const status = ref("");
const kind = ref("");
const range = ref<DateRange>(thisMonth());
const filter = computed<CommissionFilter>(() => ({
  affiliate_id: affiliate.value,
  status: status.value,
  kind: kind.value,
  payout_id: props.payoutId ?? "",
  from: dayStart(range.value.from) ?? "",
  to: dayEnd(range.value.to) ?? "",
}));
const pager = usePager();
watch(filter, () => pager.reset());
const active = computed(() => affiliate.value !== "" || status.value !== "" || kind.value !== "" || rangeChanged(range.value));
function clear() {
  affiliate.value = status.value = kind.value = "";
  range.value = thisMonth();
}

const options = useAffiliateOptions(tenantId);
const list = useCommissions(tenantId, filter, pager.token, pager.size);

const reversing = ref<Commission | null>(null);
const reverseOpen = ref(false);
function reverse(c: Commission) {
  reversing.value = c;
  reverseOpen.value = true;
}
/** Only a commission that still has something left can be taken back; a reversal row is the taking back itself. */
const partnerOptions = computed(() => [{ value: "", label: t("affiliates.commissions.allPartners") }, ...(options.data.value ?? []).map((a) => ({ value: a.id ?? "", label: a.code ?? "" }))]);
const statusOptions = computed(() => [{ value: "", label: t("affiliates.commissions.allStatuses") }, ...["pending", "approved", "paid", "reversed"].map((s) => ({ value: s, label: t(`affiliates.status.commission.${s}`) }))]);
const kindOptions = computed(() => [
  { value: "", label: t("affiliates.commissions.allKinds") },
  { value: "commission", label: t("affiliates.commissions.kinds.commission") },
  { value: "reversal", label: t("affiliates.commissions.kinds.reversal") },
]);
const items = computed(() => list.data.value?.items ?? []);
/** The sheet steps through the loaded page and, at its ends, loads the page beyond. */
const detail = useEdgeDetail(items, { hasPrev: pager.hasPrev, hasNext: () => !!list.data.value?.next, prev: pager.prev, next: () => pager.next(list.data.value?.next ?? "") }, props.payoutId
    ? undefined
    : {
        kind: "commission",
        id: (c) => c.id ?? "",
        ready: () => list.isSuccess.value,
        rewind: pager.reset,
        // The date range starts on this month: a commission of an earlier one is looked for with no range.
        missing: () => {
          if (range.value.from === "" && range.value.to === "") return false;
          range.value = { from: "", to: "" };
          return true;
        },
      },
);
const index = detail.index;
const current = detail.row;
// Taking a commission back is in the address too, found among the commissions on screen.
useSheetTarget("reverse-commission", reverseOpen, reversing, { id: (c) => c.id ?? "", find: (id) => items.value.find((c) => c.id === id) });
function reverseFrom(c: Commission) {
  index.value = null;
  reverse(c);
}
const canReverse = (c: Commission) => !isReversal(c) && c.status !== "reversed" && !!c.invoice_id;
</script>

<template>
  <Card class="p-3" data-testid="commissions">
    <FilterBar v-if="!payoutId" :active="active" class="pt-2" @clear="clear">
      <FilterSelect v-model="affiliate" :label="t('affiliates.commissions.partner')" :options="partnerOptions" />
      <FilterSelect v-model="status" :label="t('affiliates.common.status')" :options="statusOptions" />
      <FilterSelect v-model="kind" :label="t('affiliates.commissions.kind')" :options="kindOptions" />
      <DateRangePicker v-model="range" />
    </FilterBar>
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
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow
          v-for="(c, i) in list.data.value.items"
          :key="c.id"
          :data-kind="c.kind"
          :class="[isReversal(c) ? 'bg-destructive-soft/40' : '', payoutId ? '' : 'cursor-pointer']"
          :data-state="index === i ? 'selected' : undefined"
          @click="payoutId ? undefined : (index = i)"
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
            <Button v-if="c.payout_id" type="button" variant="link" size="sm" class="mono h-auto p-0" :aria-label="t('affiliates.commissions.openPayout')" @click.stop="$emit('payout', c.payout_id)">{{ c.payout_id.slice(0, 8) }}</Button>
            <span v-else class="text-muted-foreground">—</span>
          </TableCell>
          <TableCell class="text-end">
            <RowActions :actions="canReverse(c) ? [{ key: 'reverse', label: t('affiliates.commissions.reverse'), icon: Undo2, disabled: !canWrite, onSelect: () => reverse(c) }] : []" />
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>
  <DetailSheet v-if="!payoutId" v-model:index="index" :total="items.length" :offset="pager.offset.value" :of="list.data.value?.total" :edge="detail.edge.value" :title="current?.affiliate_code ?? ''" :description="current ? fmt.date(current.earn_time, 'short') : undefined" @edge="detail.onEdge">
    <dl v-if="current" class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm" data-testid="commission-detail">
      <dt class="text-muted-foreground">{{ t("affiliates.commissions.amount") }}</dt>
      <dd class="text-end text-lg font-semibold tabular-nums" :class="isReversal(current) ? 'text-destructive' : ''">{{ fmt.amount(current.amount_minor, current.currency) }}</dd>
      <dt class="text-muted-foreground">{{ t("affiliates.common.status") }}</dt>
      <dd class="flex flex-wrap justify-end gap-1">
        <StatusPill v-if="isReversal(current)" tone="danger">{{ t("affiliates.commissions.kinds.reversal") }}</StatusPill>
        <StatusPill :tone="commissionTone(current.status)">{{ t(`affiliates.status.commission.${current.status}`) }}</StatusPill>
      </dd>
      <dt class="text-muted-foreground">{{ t("affiliates.commissions.base") }}</dt>
      <dd class="text-end tabular-nums">{{ fmt.amount(current.net_minor, current.currency) }}</dd>
      <dt class="text-muted-foreground">{{ t("affiliates.commissions.invoice") }}</dt>
      <dd class="text-end">{{ current.invoice_number || current.invoice_id || "—" }}</dd>
      <dt class="text-muted-foreground">{{ t("billing.creditNotes.customer") }}</dt>
      <dd class="text-end" dir="auto">{{ current.customer_external_id || "—" }}</dd>
      <template v-if="!isReversal(current) && current.reversed_minor && current.reversed_minor !== '0'">
        <dt class="text-muted-foreground">{{ t("affiliates.commissions.kinds.reversal") }}</dt>
        <dd class="text-destructive text-end tabular-nums">{{ fmt.amount(current.reversed_minor, current.currency) }}</dd>
      </template>
      <template v-if="current.payout_id">
        <dt class="text-muted-foreground">{{ t("affiliates.commissions.payout") }}</dt>
        <dd class="text-end"><Button type="button" variant="link" size="sm" class="mono h-auto p-0" @click="index = null; emit('payout', current.payout_id!)">{{ current.payout_id.slice(0, 8) }}</Button></dd>
      </template>
    </dl>
    <template v-if="current && canReverse(current)" #footer>
      <Button type="button" variant="outline" :disabled="!canWrite" @click="reverseFrom(current)">{{ t("affiliates.commissions.reverse") }}</Button>
    </template>
  </DetailSheet>
  <ReverseDialog v-model:open="reverseOpen" :tenant-id="tenantId" :commission="reversing" />
</template>
