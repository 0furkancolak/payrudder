<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { Ban, ArrowUpRight, Ticket } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { askConfirm } from "@/shared/lib/confirm";
import { useBulk } from "@/shared/lib/bulk";
import { useTableState } from "@/shared/lib/useTableState";
import { SortSelect } from "@/shared/ui/sort-select";
import { countHigh, nameAsc, nameDesc, newest, oldest, time } from "@/shared/lib/sortDefs";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { BulkBar, RowCheck } from "@/shared/ui/bulk";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorState } from "@/shared/ui/error-state";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCouponActions, useCoupons, type Coupon } from "./api";
import CouponDetail from "./CouponDetail.vue";
import CouponSheet from "./CouponSheet.vue";
import { describeCoupon, describeDuration, describeWhere, TARGETS } from "./promotions";

/** The coupons as a table: a row opens the coupon with its promotion codes and redemptions; new coupons are made in a sheet. */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ newCode: [string] }>();
const { t, locale } = useI18n();
const canWrite = useCanWrite();

const coupons = useCoupons(() => props.tenantId);
const actions = useCouponActions(() => props.tenantId);
const creating = ref(false);

const list = computed<Coupon[]>(() => coupons.data.value?.coupons ?? []);
const table = useTableState<Coupon>(list, {
  sheet: { kind: "coupon", id: (c) => c.code }, url: "coupons-", searchFields: (c) => [c.code, c.name],
  sorts: () => [newest(t, (c) => time(c.create_time)), oldest(t, (c) => time(c.create_time)), nameAsc(t, (c) => c.code), nameDesc(t, (c) => c.code), countHigh(t, (c) => c.redeemed)],
  filters: {
    state: (c, v) => (v === "active" ? !!c.active : !c.active),
    target: (c, v) => (c.applies_to ?? []).length === 0 || (c.applies_to ?? []).includes(v as never),
  },
});
const filter = (key: string) => computed({ get: () => table.filters.value[key] ?? [], set: (v: string[]) => (table.filters.value = { ...table.filters.value, [key]: v }) });
const stateFilter = filter("state");
const targetFilter = filter("target");
const stateOptions = computed(() => [
  { value: "", label: t("billing.pricing.promotions.codes.stateAll") },
  { value: "active", label: t("billing.pricing.coupons.active") },
  { value: "inactive", label: t("billing.pricing.coupons.inactive") },
]);
const targetOptions = computed(() => [{ value: "", label: t("billing.coupons.anyTarget") }, ...TARGETS.map((x) => ({ value: x.value, label: t(`billing.pricing.promotions.targets.${x.key}`) }))]);

// Deactivating several at once: the ticked coupons that are still active. A coupon cannot be switched on again, so it asks first.
const bulk = useBulk(table, { name: (c) => c.code ?? "" });
const bulkDeactivate = () =>
  bulk.run((c) => actions.deactivate.mutateAsync(c.code ?? ""), {
    rows: (all) => all.filter((c) => c.active),
    confirm: (rows) => ({ title: t("billing.pricing.coupons.bulk.title", { n: rows.length }, rows.length), body: t("billing.pricing.coupons.bulk.confirm", { n: rows.length }, rows.length), confirmLabel: t("billing.pricing.coupons.deactivate") }),
  });

/** A coupon cannot be switched on again, so a single one asks as the bulk action does. */
async function deactivate(code: string | undefined) {
  if (code && (await askConfirm({ title: t("billing.pricing.coupons.confirmTitle", { code }), body: t("billing.pricing.coupons.bulk.confirm"), confirmLabel: t("billing.pricing.coupons.deactivate") }))) actions.deactivate.mutate(code);
}

const current = table.row;
const actionsOf = (c: Coupon, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) },
  ...(c.active && canWrite.value ? [{ key: "code", label: t("billing.pricing.promotions.codes.create"), icon: Ticket, onSelect: () => emit("newCode", c.code ?? "") }] : []),
  ...(c.active && canWrite.value ? [{ key: "deactivate", label: t("billing.pricing.coupons.deactivate"), icon: Ban, destructive: true, separatorBefore: true, onSelect: () => deactivate(c.code) }] : []),
];
</script>

<template>
  <Card class="p-3" data-testid="coupons-list">
    <ErrorState :error="coupons.error.value" :retrying="coupons.isFetching.value" compact @retry="coupons.refetch()" />
    <ErrorText :error="actions.deactivate.error.value" class="px-3 pb-2" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('billing.coupons.searchCoupons')" @clear="table.clear()">
      <FilterSelect v-model="stateFilter" multiple :label="t('billing.subscriptions.state')" :options="stateOptions" />
      <FilterSelect v-model="targetFilter" multiple :label="t('billing.pricing.promotions.appliesTo')" :options="targetOptions" />
      <SortSelect v-if="table.sorts.value.length > 0" v-model="table.sort.value" :options="table.sorts.value" />
      <HeaderAction v-if="canWrite" test-id="coupons-new" :label="t('billing.coupons.new')" @click="creating = true" />
    </DataToolbar>
    <BulkBar v-if="canWrite" v-bind="bulk.bar.value" @clear="table.clearSelection()" @select-all="bulk.selectAll()">
      <Button type="button" size="sm" variant="outline" :disabled="bulk.busy.value" data-testid="bulk-deactivate" @click="bulkDeactivate">{{ t("billing.pricing.coupons.bulk.deactivate") }}</Button>
    </BulkBar>
    <p v-if="bulk.note.value" class="text-muted-foreground px-4 pb-2 text-xs" role="status" data-testid="bulk-note">{{ bulk.note.value }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead v-if="canWrite" class="w-10"><RowCheck :label="t('common.bulk.selectPage')" :checked="table.allOnPage.value" :indeterminate="table.someOnPage.value && !table.allOnPage.value" @change="table.togglePage()" /></TableHead>
          <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
          <TableHead>{{ t("billing.coupons.discount") }}</TableHead>
          <TableHead class="max-lg:hidden">{{ t("billing.pricing.promotions.appliesTo") }}</TableHead>
          <TableHead class="max-md:hidden">{{ t("billing.pricing.coupons.duration") }}</TableHead>
          <TableHead class="text-end max-md:hidden">{{ t("billing.pricing.promotions.codeCount") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.coupons.redeemed") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
          <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(c, i) in table.items.value" :key="c.code" class="cursor-pointer" :data-state="table.isOpen(i) ? 'selected' : undefined" :data-testid="`coupon-${c.code}`" @click="table.open(i)">
          <TableCell v-if="canWrite"><RowCheck :label="`${t('common.bulk.selectRow')} ${c.code}`" :checked="table.isTicked(c)" @change="(on) => table.tick(c, on)" /></TableCell>
          <TableCell>
            <span class="mono font-medium">{{ c.code }}</span>
            <span v-if="c.name" class="text-muted-foreground block text-xs">{{ c.name }}</span>
          </TableCell>
          <TableCell class="font-medium tabular-nums">{{ describeCoupon(c, t, locale) }}</TableCell>
          <TableCell class="text-xs max-lg:hidden">{{ describeWhere(c, t) }}</TableCell>
          <TableCell class="max-md:hidden">{{ describeDuration(c, t) }}</TableCell>
          <TableCell class="text-end tabular-nums max-md:hidden">{{ c.promotion_code_count ?? 0 }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ c.redeemed }}<template v-if="c.max_redemptions"> / {{ c.max_redemptions }}</template></TableCell>
          <TableCell><Badge :variant="c.active ? 'success' : 'secondary'">{{ c.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></TableCell>
          <TableCell><RowActions :actions="actionsOf(c, i)" :label="`${t('common.moreActions')} ${c.code}`" /></TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <Skeleton v-if="coupons.isPending.value" class="mx-3 my-4 h-24" />
    <TableEmpty v-else-if="list.length === 0 && !coupons.error.value">{{ t("billing.pricing.coupons.empty") }}</TableEmpty>
    <TableEmpty v-else-if="list.length > 0 && table.total.value === 0">{{ t("common.noMatches") }}</TableEmpty>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.code ?? ''" :description="current ? [current.name, describeCoupon(current, t, locale)].filter(Boolean).join(' · ') : undefined" wide>
    <CouponDetail v-if="current" :key="current.code" :tenant-id="tenantId" :coupon="current" @new-code="(c) => emit('newCode', c)" />
    <template v-if="current?.active && canWrite" #footer>
      <Button type="button" variant="outline" :disabled="actions.deactivate.isPending.value" @click="deactivate(current?.code)"><Ban class="size-4" aria-hidden="true" />{{ t("billing.pricing.coupons.deactivate") }}</Button>
    </template>
  </DetailSheet>

  <CouponSheet v-model:open="creating" :tenant-id="tenantId" />
</template>
