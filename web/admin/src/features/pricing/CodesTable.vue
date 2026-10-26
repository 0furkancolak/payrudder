<script setup lang="ts">
import { Ban } from "@lucide/vue";
import { useI18n } from "vue-i18n";
import { useCanWrite } from "@/shared/lib/access";
import { useFormat } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { RowActions } from "@/shared/ui/row-actions";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import type { PromotionCode } from "./api";
import { codeRules } from "./promotions";

/** Promotion codes as rows: the code, how often it was used, its expiry and rules. A row opens (the page shows its sheet); an active one can be deactivated. */
defineProps<{ rows: readonly PromotionCode[]; selected?: number | null; showCoupon?: boolean }>();
const emit = defineEmits<{ select: [number]; deactivate: [string] }>();
const { t, locale } = useI18n();
const fmt = useFormat();
const canWrite = useCanWrite();
</script>

<template>
  <Table>
    <TableHeader>
      <TableRow class="hover:bg-transparent">
        <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
        <TableHead v-if="showCoupon" class="max-md:hidden">{{ t("billing.pricing.promotions.redemptions.coupon") }}</TableHead>
        <TableHead class="text-end">{{ t("billing.pricing.promotions.codes.used") }}</TableHead>
        <TableHead class="max-md:hidden">{{ t("billing.pricing.promotions.codes.expiresCol") }}</TableHead>
        <TableHead class="max-lg:hidden">{{ t("billing.pricing.promotions.codes.rules") }}</TableHead>
        <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
        <TableHead class="w-px"><span class="sr-only">{{ t("common.moreActions") }}</span></TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="(c, i) in rows" :key="c.id" class="cursor-pointer" :data-state="selected === i ? 'selected' : undefined" :data-testid="`code-${c.code}`" @click="emit('select', i)">
        <TableCell class="mono font-medium">{{ c.code }}</TableCell>
        <TableCell v-if="showCoupon" class="mono text-xs max-md:hidden">{{ c.coupon_code }}</TableCell>
        <TableCell class="text-end tabular-nums">{{ c.redeemed ?? 0 }}<template v-if="c.max_redemptions"> / {{ c.max_redemptions }}</template></TableCell>
        <TableCell class="text-xs max-md:hidden">{{ fmt.date(c.expire_time, "day", "—") }}</TableCell>
        <TableCell class="text-xs max-lg:hidden">{{ codeRules(c, t, locale).join(" · ") || "—" }}</TableCell>
        <TableCell><Badge :variant="c.active ? 'success' : 'secondary'">{{ c.active ? t("billing.pricing.coupons.active") : t("billing.pricing.coupons.inactive") }}</Badge></TableCell>
        <TableCell>
          <RowActions :actions="c.active ? [{ key: 'deactivate', label: t('billing.pricing.coupons.deactivate'), icon: Ban, disabled: !canWrite, onSelect: () => emit('deactivate', c.code ?? '') }] : []" :label="`${t('common.moreActions')} ${c.code}`" />
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
</template>
