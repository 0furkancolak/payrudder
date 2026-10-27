<script setup lang="ts">
import { Ban, ArrowUpRight, Plus, Ticket } from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useTableState } from "@/shared/lib/useTableState";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { RowActions } from "@/shared/ui/row-actions";
import { Skeleton } from "@/shared/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
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
  sheet: { kind: "coupon", id: (c) => c.code }, searchFields: (c) => [c.code, c.name],
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

const current = table.row;
const actionsOf = (c: Coupon, i: number) => [
  { key: "view", label: t("common.view"), icon: ArrowUpRight, onSelect: () => table.open(i) },
  ...(c.active ? [{ key: "code", label: t("billing.pricing.promotions.codes.create"), icon: Ticket, disabled: !canWrite.value, onSelect: () => emit("newCode", c.code ?? "") }] : []),
  ...(c.active ? [{ key: "deactivate", label: t("billing.pricing.coupons.deactivate"), icon: Ban, destructive: true, separatorBefore: true, disabled: !canWrite.value, onSelect: () => actions.deactivate.mutate(c.code ?? "") }] : []),
];
</script>

<template>
  <Card class="p-3" data-testid="coupons-list">
    <ErrorText :error="coupons.error.value" class="px-3 pb-2" />
    <ErrorText :error="actions.deactivate.error.value" class="px-3 pb-2" />
    <DataToolbar v-model:search="table.search.value" :active="table.active.value" :placeholder="t('billing.coupons.searchCoupons')" @clear="table.clear()">
      <FilterSelect v-model="stateFilter" multiple :label="t('billing.subscriptions.state')" :options="stateOptions" />
      <FilterSelect v-model="targetFilter" multiple :label="t('billing.pricing.promotions.appliesTo')" :options="targetOptions" />
      <template #actions>
        <Button type="button" data-testid="coupons-new" :disabled="!canWrite" @click="creating = true"><Plus class="size-4" aria-hidden="true" />{{ t("billing.coupons.new") }}</Button>
      </template>
    </DataToolbar>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
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
    <p v-else-if="list.length === 0 && !coupons.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.pricing.coupons.empty") }}</p>
    <p v-else-if="list.length > 0 && table.total.value === 0" class="text-muted-foreground p-4 text-center text-sm">{{ t("common.noMatches") }}</p>
    <DataPagination v-if="list.length > 0" v-model:page="table.page.value" v-model:size="table.size.value" :pages="table.pages.value" :total="table.total.value" />
  </Card>

  <DetailSheet v-model:index="table.detail.value" :total="table.total.value" :title="current?.code ?? ''" :description="current ? [current.name, describeCoupon(current, t, locale)].filter(Boolean).join(' · ') : undefined" wide>
    <CouponDetail v-if="current" :key="current.code" :tenant-id="tenantId" :coupon="current" @new-code="(c) => emit('newCode', c)" />
    <template v-if="current?.active && canWrite" #footer>
      <Button type="button" variant="outline" :disabled="actions.deactivate.isPending.value" @click="actions.deactivate.mutate(current?.code ?? '')"><Ban class="size-4" aria-hidden="true" />{{ t("billing.pricing.coupons.deactivate") }}</Button>
    </template>
  </DetailSheet>

  <CouponSheet v-model:open="creating" :tenant-id="tenantId" />
</template>
