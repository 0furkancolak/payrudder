<script setup lang="ts">
import { Plus } from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { DetailList } from "@/shared/ui/detail-list";
import { ErrorText } from "@/shared/ui/error-text";
import { Skeleton } from "@/shared/ui/skeleton";
import { useCodeActions, usePromotionCodes, useRedemptions, type Coupon } from "./api";
import CodesTable from "./CodesTable.vue";
import { describeCoupon, describeDuration, describeWhere } from "./promotions";
import RedemptionsTable from "./RedemptionsTable.vue";

/** One coupon: what it is, the promotion codes that point to it and who used it (customer, invoice, date, amount). */
const props = defineProps<{ tenantId: string; coupon: Coupon }>();
const emit = defineEmits<{ newCode: [string] }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();

const code = computed(() => props.coupon.code ?? "");
const codes = usePromotionCodes(() => props.tenantId, code, "", "", "", 10);
const redemptions = useRedemptions(() => props.tenantId, code, "", 10);
const act = useCodeActions(() => props.tenantId);

const rows = computed(() => {
  const c = props.coupon;
  return [
    { key: "value", label: t("billing.coupons.discount"), value: describeCoupon(c, t, locale.value) },
    { key: "name", label: t("billing.pricing.coupons.name"), value: c.name },
    { key: "where", label: t("billing.pricing.promotions.appliesTo"), value: describeWhere(c, t) },
    { key: "duration", label: t("billing.pricing.coupons.duration"), value: describeDuration(c, t) },
    { key: "redeemed", label: t("billing.pricing.coupons.redeemed"), value: c.max_redemptions ? `${c.redeemed ?? 0} / ${c.max_redemptions}` : String(c.redeemed ?? 0) },
    { key: "expires", label: t("billing.coupons.expires"), value: fmt.date(c.expire_time, "day", t("billing.coupons.never")) },
    { key: "state", label: t("billing.subscriptions.state") },
    { key: "created", label: t("billing.coupons.created"), value: fmt.date(c.create_time, "dateTime", "—") },
  ];
});
</script>

<template>
  <div class="flex flex-col gap-6" data-testid="coupon-detail">
    <DetailList :rows="rows">
      <template #state><Badge :variant="coupon.active ? 'success' : 'secondary'">{{ coupon.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></template>
    </DetailList>

    <section class="flex flex-col gap-3" :aria-label="t('billing.pricing.promotions.codes.title', { coupon: code })">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-base font-semibold">{{ t("billing.coupons.tabs.codes") }}</h3>
        <Button v-if="coupon.active" type="button" size="sm" variant="outline" :disabled="!canWrite" data-testid="detail-new-code" @click="emit('newCode', code)"><Plus class="size-4" aria-hidden="true" />{{ t("billing.pricing.promotions.codes.create") }}</Button>
      </div>
      <ErrorText :error="codes.error.value" />
      <ErrorText :error="act.deactivate.error.value" />
      <Skeleton v-if="codes.isPending.value" class="h-16 w-full" />
      <p v-else-if="(codes.data.value?.codes ?? []).length === 0" class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.codes.none") }}</p>
      <CodesTable v-else :rows="codes.data.value?.codes ?? []" @deactivate="(c) => act.deactivate.mutate(c)" />
      <p v-if="(codes.data.value?.total ?? 0) > (codes.data.value?.codes ?? []).length" class="text-muted-foreground text-xs">{{ t("billing.coupons.firstOf", { n: (codes.data.value?.codes ?? []).length, total: codes.data.value?.total }) }}</p>
    </section>

    <section class="flex flex-col gap-3" :aria-label="t('billing.coupons.tabs.redemptions')">
      <h3 class="text-base font-semibold">{{ t("billing.coupons.tabs.redemptions") }}</h3>
      <ErrorText :error="redemptions.error.value" />
      <Skeleton v-if="redemptions.isPending.value" class="h-16 w-full" />
      <p v-else-if="(redemptions.data.value?.redemptions ?? []).length === 0" class="text-muted-foreground text-sm">{{ t("billing.pricing.promotions.redemptions.none") }}</p>
      <RedemptionsTable v-else :rows="redemptions.data.value?.redemptions ?? []" :tenant-id="tenantId" :show-coupon="false" />
      <p v-if="(redemptions.data.value?.total ?? 0) > (redemptions.data.value?.redemptions ?? []).length" class="text-muted-foreground text-xs">{{ t("billing.coupons.firstOf", { n: (redemptions.data.value?.redemptions ?? []).length, total: redemptions.data.value?.total }) }}</p>
    </section>
  </div>
</template>
