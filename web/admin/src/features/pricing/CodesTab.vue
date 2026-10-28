<script setup lang="ts">
import { HeaderAction } from "@/shared/ui/page-header";
import { Ban } from "@lucide/vue";
import { refDebounced } from "@vueuse/core";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Card } from "@/shared/ui/card";
import { DataPagination, DataToolbar } from "@/shared/ui/data-table";
import { DetailList } from "@/shared/ui/detail-list";
import { DetailSheet } from "@/shared/ui/detail-sheet";
import { ErrorText } from "@/shared/ui/error-text";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Skeleton } from "@/shared/ui/skeleton";
import { PAGE_SIZES } from "@/shared/lib/paging";
import { useCodeActions, useCoupons, usePromotionCodes, useRedemptions } from "./api";
import CodesTable from "./CodesTable.vue";
import { codeRules, describeCoupon } from "./promotions";
import RedemptionsTable from "./RedemptionsTable.vue";

/** Every promotion code of the tenant, a page at a time, with the coupon each points to. A row opens the code with its rules and its redemptions. */
const props = defineProps<{ tenantId: string }>();
const emit = defineEmits<{ newCode: [string] }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

const coupon = ref("");
const state = ref("");
const search = ref("");
const typed = refDebounced(search, 250);
const pageToken = ref("");
const history = ref<string[]>([]);
const size = ref<number>(PAGE_SIZES[0]);
const codes = usePromotionCodes(() => props.tenantId, coupon, state, typed, pageToken, size);
const coupons = useCoupons(() => props.tenantId);
const act = useCodeActions(() => props.tenantId);
const rows = computed(() => codes.data.value?.codes ?? []);
watch([coupon, state, typed, size], () => {
  pageToken.value = "";
  history.value = [];
  index.value = null;
});
const couponOptions = computed(() => [{ value: "", label: t("billing.pricing.promotions.redemptions.allCoupons") }, ...(coupons.data.value?.coupons ?? []).map((c) => ({ value: c.code ?? "" }))]);
const stateOptions = computed(() => [
  { value: "", label: t("billing.pricing.promotions.codes.stateAll") },
  { value: "active", label: t("billing.pricing.coupons.active") },
  { value: "inactive", label: t("billing.pricing.coupons.inactive") },
]);
const active = computed(() => coupon.value !== "" || state.value !== "" || search.value !== "");
function clear() {
  coupon.value = state.value = search.value = "";
}
function next() {
  const token = codes.data.value?.next_page_token;
  if (!token) return;
  history.value.push(pageToken.value);
  pageToken.value = token;
  index.value = null;
}
function back() {
  pageToken.value = history.value.pop() ?? "";
  index.value = null;
}

// The open code.
const index = ref<number | null>(null);
const current = computed(() => (index.value === null ? undefined : rows.value[index.value]));
const parent = computed(() => (coupons.data.value?.coupons ?? []).find((c) => c.code === current.value?.coupon_code));
const used = useRedemptions(() => props.tenantId, () => ({ promotionCode: current.value?.code ?? "" }), "", 10, () => !!current.value);
const usedRows = computed(() => (current.value ? (used.data.value?.redemptions ?? []) : []));
const detailRows = computed(() => {
  const c = current.value;
  if (!c) return [];
  return [
    { key: "code", label: t("billing.pricing.coupons.code"), value: c.code, mono: true },
    { key: "coupon", label: t("billing.pricing.promotions.redemptions.coupon"), value: parent.value ? `${c.coupon_code} · ${describeCoupon(parent.value, t, locale.value)}` : c.coupon_code },
    { key: "used", label: t("billing.pricing.promotions.codes.used"), value: c.max_redemptions ? `${c.redeemed ?? 0} / ${c.max_redemptions}` : String(c.redeemed ?? 0) },
    { key: "expires", label: t("billing.coupons.expires"), value: fmt.date(c.expire_time, "day", t("billing.coupons.never")) },
    { key: "rules", label: t("billing.pricing.promotions.codes.rules"), value: codeRules(c, t, locale.value).join("\n") },
    { key: "state", label: t("billing.subscriptions.state") },
    { key: "created", label: t("billing.coupons.created"), value: fmt.date(c.create_time, "dateTime", "—") },
  ];
});
</script>

<template>
  <Card class="p-3" data-testid="codes-list">
    <ErrorText :error="codes.error.value" class="px-3 pb-2" />
    <ErrorText :error="act.deactivate.error.value" class="px-3 pb-2" />
    <DataToolbar v-model:search="search" :placeholder="t('billing.pricing.promotions.codes.search')" :active="active" @clear="clear">
      <FilterSelect v-model="coupon" :label="t('billing.pricing.promotions.redemptions.coupon')" :options="couponOptions" />
      <FilterSelect v-model="state" :label="t('billing.subscriptions.state')" :options="stateOptions" />
      <HeaderAction data-testid="codes-new" :disabled="!canWrite" :label="t('billing.pricing.promotions.codes.create')" @click="emit('newCode', coupon)" />
    </DataToolbar>
    <CodesTable :rows="rows" :selected="index" show-coupon @select="(i) => (index = i)" @deactivate="(c) => act.deactivate.mutate(c)" />
    <Skeleton v-if="codes.isPending.value" class="mx-3 my-4 h-24" />
    <p v-else-if="rows.length === 0 && !codes.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ active ? t("common.noMatches") : t("billing.pricing.promotions.codes.none") }}</p>
    <DataPagination v-model:size="size" :page="history.length + 1" :total="codes.data.value?.total ?? 0" :has-prev="history.length > 0" :has-next="!!codes.data.value?.next_page_token" @prev="back" @next="next" />
  </Card>

  <DetailSheet v-model:index="index" :total="rows.length" :title="current?.code ?? ''" :description="current?.coupon_code" wide>
    <div v-if="current" class="flex flex-col gap-6" data-testid="code-detail">
      <DetailList :rows="detailRows">
        <template #rules>
          <ul v-if="codeRules(current, t, locale).length" class="grid gap-1"><li v-for="r in codeRules(current, t, locale)" :key="r">{{ r }}</li></ul>
          <span v-else>—</span>
        </template>
        <template #state><Badge :variant="current.active ? 'success' : 'secondary'">{{ current.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></template>
      </DetailList>
      <section class="flex flex-col gap-3" :aria-label="t('billing.coupons.tabs.redemptions')">
        <h3 class="text-base font-semibold">{{ t("billing.coupons.tabs.redemptions") }}</h3>
        <ErrorText :error="used.error.value" />
        <Skeleton v-if="used.isPending.value" class="h-16 w-full" />
        <p v-else-if="usedRows.length === 0" class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.redemptions.none") }}</p>
        <RedemptionsTable v-else :rows="usedRows" :tenant-id="tenantId" :show-coupon="false" />
      </section>
    </div>
    <template v-if="current?.active && canWrite" #footer>
      <Button type="button" variant="outline" :disabled="act.deactivate.isPending.value" @click="act.deactivate.mutate(current?.code ?? '')"><Ban class="size-4" aria-hidden="true" />{{ t("billing.pricing.coupons.deactivate") }}</Button>
    </template>
  </DetailSheet>
</template>
