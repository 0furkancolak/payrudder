<script setup lang="ts">
import { Link } from "@tanstack/vue-router";
import { useI18n } from "vue-i18n";
import { useFormat } from "@/shared/lib/format";
import { tenantBase } from "@/shared/lib/session";
import { Badge } from "@/shared/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/shared/ui/table";
import type { Redemption } from "./api";
import { targetKey } from "./promotions";

/** The redemptions of a coupon as rows: when, which code, who, on what, the invoice it landed on and what it took off. A row opens (the page shows its sheet). */
defineProps<{ rows: readonly Redemption[]; tenantId: string; selected?: number | null; showCoupon?: boolean }>();
const emit = defineEmits<{ select: [number] }>();
const { t } = useI18n();
const fmt = useFormat();

const amount = (r: Redemption) => (r.discount_minor && r.discount_minor !== "0" ? fmt.money(r.discount_minor, r.currency) : r.bonus_credits && r.bonus_credits !== "0" ? t("billing.coupons.redemptions.bonusCredits", { n: r.bonus_credits }) : "—");
</script>

<template>
  <Table>
    <TableHeader>
      <TableRow class="hover:bg-transparent">
        <TableHead>{{ t("billing.accounting.form.date") }}</TableHead>
        <TableHead>{{ t("billing.pricing.coupons.code") }}</TableHead>
        <TableHead>{{ t("billing.pricing.promotions.redemptions.customer") }}</TableHead>
        <TableHead class="max-md:hidden">{{ t("billing.pricing.promotions.redemptions.usedOn") }}</TableHead>
        <TableHead class="max-md:hidden">{{ t("billing.coupons.redemptions.invoice") }}</TableHead>
        <TableHead class="text-end">{{ t("billing.pricing.promotions.redemptions.discount") }}</TableHead>
        <TableHead>{{ t("billing.subscriptions.state") }}</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="(r, i) in rows" :key="r.id" class="cursor-pointer" :data-state="selected === i ? 'selected' : undefined" :data-testid="`redemption-${r.id}`" @click="emit('select', i)">
        <TableCell class="text-xs">{{ fmt.date(r.create_time, "short", "—") }}</TableCell>
        <TableCell class="mono">{{ r.promotion_code || r.coupon_code }}<span v-if="showCoupon !== false && r.promotion_code && r.promotion_code !== r.coupon_code" class="text-muted-foreground block text-xs">{{ r.coupon_code }}</span></TableCell>
        <TableCell>{{ r.customer_external_id || "—" }}</TableCell>
        <TableCell class="text-xs max-md:hidden">{{ t(`billing.pricing.promotions.targets.${targetKey(r.target)}`) }}</TableCell>
        <TableCell class="max-md:hidden">
          <Link v-if="r.invoice_id" :to="`${tenantBase()}/$id/invoices` as never" :params="{ id: tenantId } as never" :search="{ invoice: r.invoice_id } as never" class="mono text-xs hover:underline" @click.stop>{{ r.invoice_id.slice(0, 8) }}</Link>
          <span v-else class="text-muted-foreground">—</span>
        </TableCell>
        <TableCell class="text-end tabular-nums">{{ amount(r) }}</TableCell>
        <TableCell>
          <Badge v-if="r.reverse_time" variant="secondary" :title="fmt.date(r.reverse_time, 'short', '')">{{ t("billing.pricing.promotions.redemptions.reversed") }}</Badge>
          <Badge v-else variant="success">{{ t("billing.pricing.promotions.redemptions.counted") }}</Badge>
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
</template>
