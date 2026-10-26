<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useErrorText } from "@/shared/lib/errors";
import { formatMinor } from "@/shared/lib/money";
import { Badge } from "@/shared/ui/badge";
import { Card } from "@/shared/ui/card";
import { X } from "@lucide/vue";
import { Button } from "@/shared/ui/button";
import { DataPagination } from "@/shared/ui/data-table";
import { FilterSelect } from "@/shared/ui/filter-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import { useCoupons, useRedemptions } from "./api";
import { targetKey } from "./promotions";

const props = defineProps<{ tenantId: string }>();
const { t, locale } = useI18n();
const errorText = useErrorText();
const tenantId = computed(() => props.tenantId);

const coupon = ref("");
const pageToken = ref("");
const history = ref<string[]>([]);
const coupons = useCoupons(tenantId);
const list = useRedemptions(tenantId, coupon, pageToken);
const couponOptions = computed(() => [
  { value: "", label: t("billing.pricing.promotions.redemptions.allCoupons") },
  ...(coupons.data.value?.coupons ?? []).map((c) => ({ value: c.code ?? "", label: c.code ?? "" })),
]);
const rows = computed(() => list.data.value?.redemptions ?? []);

function filter() {
  pageToken.value = "";
  history.value = [];
}
watch(coupon, filter);
function next() {
  const token = list.data.value?.next_page_token;
  if (!token) return;
  history.value.push(pageToken.value);
  pageToken.value = token;
}
function back() {
  pageToken.value = history.value.pop() ?? "";
}
const when = (v?: string | null) => (v ? new Intl.DateTimeFormat(locale.value, { dateStyle: "short", timeStyle: "short" }).format(new Date(v)) : "—");
</script>

<template>
  <Card class="mb-6 p-3">
    <div class="px-3 pt-2">
      <h2 class="text-lg font-semibold">{{ t("billing.pricing.promotions.redemptions.title") }}</h2>
      <p class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.redemptions.hint") }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2 px-3 py-3" role="search">
      <FilterSelect v-model="coupon" :label="t('billing.pricing.promotions.redemptions.coupon')" :options="couponOptions" />
      <Button v-if="coupon !== ''" type="button" variant="ghost" size="sm" @click="coupon = ''"><X class="size-4" aria-hidden="true" />{{ t("common.clearFilters") }}</Button>
    </div>
    <p v-if="list.error.value" class="text-destructive px-3 text-sm" role="alert">{{ errorText(list.error.value) }}</p>
    <Table>
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <TableHead>{{ t("billing.accounting.form.date") }}</TableHead>
          <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
          <TableHead>{{ t("billing.pricing.promotions.redemptions.customer") }}</TableHead>
          <TableHead>{{ t("billing.pricing.promotions.redemptions.usedOn") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.promotions.redemptions.discount") }}</TableHead>
          <TableHead class="text-end">{{ t("billing.pricing.promotions.redemptions.bonus") }}</TableHead>
          <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="r in rows" :key="r.id">
          <TableCell class="text-xs">{{ when(r.create_time) }}</TableCell>
          <TableCell class="mono">{{ r.promotion_code || r.coupon_code }}<span v-if="r.promotion_code" class="text-muted-foreground block text-xs">{{ r.coupon_code }}</span></TableCell>
          <TableCell>{{ r.customer_external_id || "—" }}</TableCell>
          <TableCell class="text-xs">{{ t(`billing.pricing.promotions.targets.${targetKey(r.target)}`) }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.discount_minor && r.discount_minor !== "0" ? formatMinor(r.discount_minor, r.currency, locale) : "—" }}</TableCell>
          <TableCell class="text-end tabular-nums">{{ r.bonus_credits && r.bonus_credits !== "0" ? r.bonus_credits : "—" }}</TableCell>
          <TableCell>
            <Badge v-if="r.reverse_time" variant="secondary" :title="when(r.reverse_time)">{{ t("billing.pricing.promotions.redemptions.reversed") }}</Badge>
            <Badge v-else variant="success">{{ t("billing.pricing.promotions.redemptions.counted") }}</Badge>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <p v-if="!list.isPending.value && rows.length === 0 && !list.error.value" class="text-muted-foreground p-4 text-center text-sm">{{ t("billing.pricing.promotions.redemptions.none") }}</p>
    <DataPagination :page="history.length + 1" :total="list.data.value?.total ?? 0" :has-prev="history.length > 0" :has-next="!!list.data.value?.next_page_token" @prev="back" @next="next" />
  </Card>
</template>
