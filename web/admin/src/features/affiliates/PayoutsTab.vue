<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { toast } from "vue-sonner";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Ban, Banknote, Eye, Plus } from "@lucide/vue";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import FilterBar from "./FilterBar.vue";
import { Card } from "@/shared/ui/card";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { StatusPill } from "@/shared/ui/status-pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useAffiliateOptions, useCancelPayout, useCreatePayouts, usePayouts } from "./api";
import CommissionsTable from "./CommissionsTable.vue";
import { payoutTone, type Payout } from "./logic";
import MarkPaidDialog from "./MarkPaidDialog.vue";
import { DataPagination } from "@/shared/ui/data-table";
import { useEdgeDetail } from "@/shared/lib/useEdgeDetail";
import { usePager } from "./paging";

const props = defineProps<{ tenantId: string; focus?: string }>();
const { t } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
const tenantId = computed(() => props.tenantId);

const status = ref("");
const pager = usePager();
watch(status, () => pager.reset());
const list = usePayouts(tenantId, status, pager.token, pager.size);
const options = useAffiliateOptions(tenantId);
const codeOf = (id: string | undefined) => (options.data.value ?? []).find((a) => a.id === id)?.code ?? (id ?? "").slice(0, 8);

const create = useCreatePayouts(tenantId);
const cancel = useCancelPayout(tenantId);
const result = computed(() => create.data.value);

const paying = ref<Payout | null>(null);
const payOpen = ref(false);
function pay(p: Payout) {
  paying.value = p;
  payOpen.value = true;
}

// The payout whose commissions are shown in the sheet (the payout another tab sent the reader to, or the one they opened).
const items = computed(() => list.data.value?.items ?? []);
/** The sheet steps through the loaded page and, at its ends, loads the page beyond. */
const detail = useEdgeDetail(items, { hasPrev: pager.hasPrev, hasNext: () => !!list.data.value?.next, prev: pager.prev, next: () => pager.next(list.data.value?.next ?? "") });
const index = detail.index;
const current = detail.row;
let focused = false;
watch(
  items,
  (rows) => {
    if (focused || !props.focus || rows.length === 0) return;
    focused = true;
    const at = rows.findIndex((p) => p.id === props.focus);
    if (at >= 0) index.value = at;
  },
  { immediate: true },
);
const statusOptions = computed(() => [{ value: "", label: t("affiliates.payouts.allStatuses") }, ...["open", "paid", "cancelled"].map((s) => ({ value: s, label: t(`affiliates.status.payout.${s}`) }))]);
const actionsOf = (p: Payout, i: number) => [
  { key: "commissions", label: t("affiliates.payouts.commissions"), icon: Eye, onSelect: () => (index.value = i) },
  ...(p.status === "open"
    ? [
        { key: "markPaid", label: t("affiliates.payouts.markPaid"), icon: Banknote, disabled: !canWrite.value, onSelect: () => pay(p) },
        { key: "cancel", label: t("affiliates.payouts.cancel"), icon: Ban, destructive: true, separatorBefore: true, disabled: !canWrite.value || cancel.isPending.value, onSelect: () => cancel.mutate(p.id ?? "") },
      ]
    : []),
];
/** The sheet closes before the dialog opens over it. */
function payFrom(p: Payout) {
  index.value = null;
  pay(p);
}

function createAll() {
  create.mutate(undefined, {
    onSuccess: (r) => {
      if ((r.payouts ?? []).length === 0 && (r.skipped ?? []).length === 0) toast.info(t("affiliates.payouts.nothing"));
    },
  });
}
</script>

<template>
  <p class="text-muted-foreground text-sm">{{ t("affiliates.payouts.intro") }}</p>
  <ErrorText :error="create.error.value" />
  <ErrorText :error="cancel.error.value" />

  <Card v-if="result" data-testid="payout-result" role="status">
    <h3 class="text-base font-semibold">{{ t("affiliates.payouts.result") }}</h3>
    <p class="m-0 text-sm" data-testid="created-count">{{ t("affiliates.payouts.created", { count: (result.payouts ?? []).length }) }}</p>
    <ul v-if="(result.payouts ?? []).length" class="m-0 list-none p-0 text-sm">
      <li v-for="p in result.payouts" :key="p.id" class="tabular-nums">
        <span class="mono font-medium">{{ p.affiliate_code }}</span> · {{ fmt.amount(p.total_minor, p.currency) }}
      </li>
    </ul>
    <template v-if="(result.skipped ?? []).length">
      <p class="m-0 text-sm font-medium">{{ t("affiliates.payouts.skipped", { count: (result.skipped ?? []).length }) }}</p>
      <ul class="m-0 list-none p-0 text-sm" data-testid="skipped">
        <li v-for="(s, i) in result.skipped" :key="i" class="tabular-nums">
          <span class="mono font-medium">{{ codeOf(s.affiliate_id) }}</span> · {{ fmt.amount(s.total_minor, s.currency) }} ·
          {{ t(`affiliates.payouts.reasons.${s.reason}`) }}
        </li>
      </ul>
    </template>
  </Card>

  <Card class="p-3">
    <FilterBar :active="status !== ''" class="pt-2" @clear="status = ''">
      <FilterSelect v-model="status" :label="t('affiliates.common.status')" :options="statusOptions" />
      <template #actions>
        <Button type="button" :disabled="!canWrite || create.isPending.value" @click="createAll"><Plus class="size-4" aria-hidden="true" />{{ t("affiliates.payouts.create") }}</Button>
      </template>
    </FilterBar>
    <ErrorText :error="list.error.value" class="px-3" />
    <Skeleton v-if="list.isPending.value" class="mx-3 h-32" />
    <p v-else-if="list.isSuccess.value && !list.data.value?.items.length" class="text-muted-foreground px-3 py-4 text-sm">{{ t("affiliates.payouts.empty") }}</p>
    <Table v-else-if="list.data.value">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("affiliates.payouts.created_at") }}</TableHead>
          <TableHead>{{ t("affiliates.commissions.partner") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.payouts.total") }}</TableHead>
          <TableHead class="text-end">{{ t("affiliates.payouts.count") }}</TableHead>
          <TableHead>{{ t("affiliates.payoutMethod") }}</TableHead>
          <TableHead>{{ t("affiliates.common.status") }}</TableHead>
          <TableHead>{{ t("affiliates.payouts.reference") }}</TableHead>
          <TableHead><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(p, i) in list.data.value.items" :key="p.id" :data-payout="p.id" class="cursor-pointer" :data-state="index === i ? 'selected' : undefined" @click="index = i">
          <TableCell class="whitespace-nowrap">{{ fmt.date(p.create_time, "short") }}</TableCell>
          <TableCell class="mono font-medium">{{ p.affiliate_code }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.amount(p.total_minor, p.currency) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ fmt.count(p.commission_count) }}</TableCell>
          <TableCell>{{ t(`affiliates.methods.${p.method ?? 'manual'}`) }}</TableCell>
          <TableCell>
            <div class="flex flex-wrap gap-1">
              <StatusPill :tone="payoutTone(p.status)">{{ t(`affiliates.status.payout.${p.status}`) }}</StatusPill>
              <StatusPill v-if="p.fake" tone="warning" data-testid="fake-badge">{{ t("affiliates.payouts.fake") }}</StatusPill>
            </div>
          </TableCell>
          <TableCell dir="auto" class="max-w-48 truncate">{{ p.reference || "—" }}</TableCell>
          <TableCell class="text-end"><RowActions :actions="actionsOf(p, i)" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <DataPagination v-if="list.data.value" v-model:size="pager.size.value" :page="pager.page.value" :has-prev="pager.hasPrev.value" :has-next="!!list.data.value.next" :total="list.data.value.total" @prev="pager.prev()" @next="pager.next(list.data.value.next)" />
  </Card>

  <DetailSheet v-model:index="index" :total="items.length" :offset="pager.offset.value" :of="list.data.value?.total" :edge="detail.edge.value" :title="t('affiliates.payouts.commissionsOf', { code: current?.affiliate_code ?? '' })" :description="current ? `${fmt.amount(current.total_minor, current.currency)} · ${t(`affiliates.status.payout.${current.status}`)}` : undefined" wide @edge="detail.onEdge">
    <div v-if="current" class="flex flex-col gap-3">
      <p v-if="current.fake" role="status" class="m-0 bg-warning-soft text-warning rounded-2xl px-4 py-3 text-sm font-medium">{{ t("affiliates.payouts.fakeNote") }}</p>
      <CommissionsTable :key="current.id" :tenant-id="tenantId" :payout-id="current.id" />
    </div>
    <template v-if="current && current.status === 'open'" #footer>
      <Button type="button" :disabled="!canWrite" @click="payFrom(current)">{{ t("affiliates.payouts.markPaid") }}</Button>
    </template>
  </DetailSheet>

  <MarkPaidDialog v-model:open="payOpen" :tenant-id="tenantId" :payout="paying" />
</template>
