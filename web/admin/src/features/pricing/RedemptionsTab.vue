<script setup lang="ts">
import PeekInvoice from "@/features/related/PeekInvoice.vue";
import { refDebounced } from "@vueuse/core";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { PAGE_SIZES } from "@/shared/lib/paging";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { useCoupons, useRedemptions } from "./api";
import { targetKey } from "./promotions";
import RedemptionsTable from "./RedemptionsTable.vue";

/** Every use of a coupon, newest first, a page at a time: narrowed by coupon or by customer. A row opens the redemption with what it took off and where it landed. */
const props = defineProps<{ tenantId: string }>();
const { t } = useI18n();
const fmt = useFormat();

const coupon = ref("");
const customer = ref("");
const typed = refDebounced(customer, 250);
const pageToken = ref("");
const history = ref<string[]>([]);
const size = ref<number>(PAGE_SIZES[0]);
const index = ref<number | null>(null);
const coupons = useCoupons(() => props.tenantId);
const list = useRedemptions(() => props.tenantId, () => ({ coupon: coupon.value, customer: typed.value.trim() }), pageToken, size);
const rows = computed(() => list.data.value?.redemptions ?? []);
watch([coupon, typed, size], () => {
  pageToken.value = "";
  history.value = [];
  index.value = null;
});
const couponOptions = computed(() => [{ value: "", label: t("billing.pricing.promotions.redemptions.allCoupons") }, ...(coupons.data.value?.coupons ?? []).map((c) => ({ value: c.code ?? "" }))]);
const active = computed(() => coupon.value !== "" || customer.value !== "");
function clear() {
  coupon.value = customer.value = "";
}
function next() {
  const token = list.data.value?.next_page_token;
  if (!token) return;
  history.value.push(pageToken.value);
  pageToken.value = token;
  index.value = null;
}
function back() {
  pageToken.value = history.value.pop() ?? "";
  index.value = null;
}

const current = computed(() => (index.value === null ? undefined : rows.value[index.value]));
const detailRows = computed(() => {
  const r = current.value;
  if (!r) return [];
  return [
    { key: "date", label: t("billing.accounting.form.date"), value: fmt.date(r.create_time, "dateTime", "—") },
    { key: "coupon", label: t("billing.pricing.promotions.redemptions.coupon"), value: r.coupon_code, mono: true },
    { key: "code", label: t("billing.pricing.coupons.code"), value: r.promotion_code || t("billing.coupons.redemptions.noCode"), mono: !!r.promotion_code },
    { key: "customer", label: t("billing.pricing.promotions.redemptions.customer"), value: r.customer_external_id },
    { key: "target", label: t("billing.pricing.promotions.redemptions.usedOn"), value: t(`billing.pricing.promotions.targets.${targetKey(r.target)}`) },
    { key: "invoice", label: t("billing.coupons.redemptions.invoice") },
    { key: "discount", label: t("billing.pricing.promotions.redemptions.discount"), value: r.discount_minor && r.discount_minor !== "0" ? fmt.money(r.discount_minor, r.currency) : "—" },
    { key: "bonus", label: t("billing.pricing.promotions.redemptions.bonus"), value: r.bonus_credits && r.bonus_credits !== "0" ? r.bonus_credits : "—" },
    { key: "state", label: t("billing.subscriptions.state") },
  ];
});
</script>

<template>
  <Card class="p-3" data-testid="redemptions-list">
    <ErrorText :error="list.error.value" class="px-3 pb-2" />
    <DataToolbar v-model:search="customer" :placeholder="t('billing.coupons.searchCustomer')" :active="active" @clear="clear">
      <FilterSelect v-model="coupon" :label="t('billing.pricing.promotions.redemptions.coupon')" :options="couponOptions" />
    </DataToolbar>
    <RedemptionsTable :rows="rows" :tenant-id="tenantId" :selected="index" @select="(i) => (index = i)" />
    <Skeleton v-if="list.isPending.value" class="mx-3 my-4 h-24" />
    <p v-else-if="rows.length === 0 && !list.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ active ? t("common.noMatches") : t("billing.pricing.promotions.redemptions.none") }}</p>
    <DataPagination v-model:size="size" :page="history.length + 1" :total="list.data.value?.total ?? 0" :has-prev="history.length > 0" :has-next="!!list.data.value?.next_page_token" @prev="back" @next="next" />
  </Card>

  <DetailSheet v-model:index="index" :total="rows.length" :title="current ? (current.promotion_code || current.coupon_code || '') : ''" :description="current?.customer_external_id" wide>
    <DetailList v-if="current" :rows="detailRows" data-testid="redemption-detail">
      <template #invoice>
        <PeekInvoice v-if="current.invoice_id" :id="current.invoice_id" :tenant-id="tenantId" class="mono text-xs hover:underline">{{ current.invoice_id }}</PeekInvoice>
        <span v-else>—</span>
      </template>
      <template #state>
        <Badge v-if="current.reverse_time" variant="secondary">{{ t("billing.pricing.promotions.redemptions.reversed") }} · {{ fmt.date(current.reverse_time, "short", "") }}</Badge>
        <Badge v-else variant="success">{{ t("billing.pricing.promotions.redemptions.counted") }}</Badge>
      </template>
    </DetailList>
  </DetailSheet>
</template>
